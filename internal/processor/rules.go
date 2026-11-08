package processor

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

const (
	settingSchemes = "decoder_schemes"
	settingRules   = "automation_rules"
)

func (p *Processor) loadPipeline() {
	var doc formatter.SchemeDoc
	if p.store.GetSetting(settingSchemes, &doc) {
		p.schemes = &doc
	} else {
		p.schemes = formatter.DefaultSchemes()
	}
	p.store.GetSetting(settingRules, &p.rules)
}

// Schemes returns the active decoder schemes.
func (p *Processor) Schemes() *formatter.SchemeDoc {
	p.rmu.RLock()
	defer p.rmu.RUnlock()
	return p.schemes
}

// SetSchemes validates, persists and activates a decoder document.
func (p *Processor) SetSchemes(doc *formatter.SchemeDoc) error {
	if err := doc.Validate(); err != nil {
		return err
	}
	doc.Updated = time.Now().UTC().Format(time.RFC3339)
	if err := p.store.SetSetting(settingSchemes, doc); err != nil {
		return err
	}
	p.rmu.Lock()
	p.schemes = doc
	p.rmu.Unlock()
	return nil
}

// ImportSchemesFile loads a decoders.json on first start, when no schemes
// have been saved yet. It reports whether anything was imported.
func (p *Processor) ImportSchemesFile(path string) (bool, error) {
	var existing formatter.SchemeDoc
	if p.store.GetSetting(settingSchemes, &existing) {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var doc formatter.SchemeDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return false, err
	}
	if err := p.SetSchemes(&doc); err != nil {
		return false, err
	}
	return true, nil
}

// usesNoSerial reports whether packets matched by a scheme are identified by
// NODE_ID alone. An explicit setting wins. Otherwise only a scheme written
// for a specific NODE_ID whose data starts before the end of the serial field
// counts, because those bytes then carry data (often a changing sequence
// number) and cannot be a serial.
func usesNoSerial(res *formatter.SchemeResult, serialEnd int) bool {
	if res == nil || res.Scheme == nil {
		return false
	}
	s := res.Scheme
	switch s.Serial {
	case "none":
		return true
	case "header":
		return false
	}
	if _, typed := res.Data["device_type"]; typed { // channel data was found right after a NODE|TYPE|SEQ header
		return true
	}
	return s.MatchType == "prefix" && s.DataOffset < serialEnd
}

// Rules returns a copy of the automation rules.
func (p *Processor) Rules() []model.Rule {
	p.rmu.RLock()
	defer p.rmu.RUnlock()
	return append([]model.Rule{}, p.rules...)
}

func validateRule(r *model.Rule) error {
	r.PayloadHex, r.PulseOffHex = strings.ToUpper(cleanHex(r.PayloadHex)), strings.ToUpper(cleanHex(r.PulseOffHex))
	if r.SourceApp == "" || r.SourceDev == "" {
		return fmt.Errorf("choose a source node")
	}
	if r.TargetApp == "" || r.TargetDev == "" {
		return fmt.Errorf("choose a target node")
	}
	if _, ok := CompareRule(r.Operator, 0, 0); !ok {
		return fmt.Errorf("unknown operator %q", r.Operator)
	}
	if r.Operator != "always" && r.Field == "" {
		return fmt.Errorf("choose the field to compare")
	}
	if b, err := hex.DecodeString(r.PayloadHex); err != nil || len(b) == 0 {
		return fmt.Errorf("downlink payload must be hex")
	}
	if _, err := hex.DecodeString(r.PulseOffHex); err != nil {
		return fmt.Errorf("pulse-off payload must be hex")
	}
	if r.PulseSec > 0 && r.PulseOffHex == "" {
		return fmt.Errorf("a pulse needs a pulse-off payload")
	}
	if r.CooldownSec < 0 || r.PulseSec < 0 {
		return fmt.Errorf("times cannot be negative")
	}
	return nil
}

// SaveRule creates or replaces a rule, keeping its run state.
func (p *Processor) SaveRule(r model.Rule) (model.Rule, error) {
	if err := validateRule(&r); err != nil {
		return r, err
	}
	p.rmu.Lock()
	defer p.rmu.Unlock()
	if r.ID == "" {
		r.ID = store.NewToken(6)
		r.LastTriggered, r.LastStatus = nil, ""
		p.rules = append(p.rules, r)
	} else {
		found := false
		for i := range p.rules {
			if p.rules[i].ID == r.ID {
				r.LastTriggered, r.LastStatus = p.rules[i].LastTriggered, p.rules[i].LastStatus
				p.rules[i], found = r, true
				break
			}
		}
		if !found {
			return r, fmt.Errorf("rule not found")
		}
	}
	return r, p.store.SetSetting(settingRules, p.rules)
}

// DeleteRule removes a rule.
func (p *Processor) DeleteRule(id string) error {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	for i := range p.rules {
		if p.rules[i].ID == id {
			p.rules = append(p.rules[:i:i], p.rules[i+1:]...)
			return p.store.SetSetting(settingRules, p.rules)
		}
	}
	return fmt.Errorf("rule not found")
}

// CompareRule applies an operator; ok is false for an unknown operator.
func CompareRule(op string, v, threshold float64) (matched, ok bool) {
	switch op {
	case ">":
		return v > threshold, true
	case "<":
		return v < threshold, true
	case ">=":
		return v >= threshold, true
	case "<=":
		return v <= threshold, true
	case "==":
		return math.Abs(v-threshold) < 0.001, true
	case "!=":
		return math.Abs(v-threshold) >= 0.001, true
	case "always":
		return true, true
	}
	return false, false
}

func ruleValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		switch strings.ToUpper(x) {
		case "ON", "TRUE":
			return 1, true
		case "OFF", "FALSE":
			return 0, true
		}
	}
	return 0, false
}

// evalRules fires every rule whose source is this node and whose condition
// holds for the decoded packet.
func (p *Processor) evalRules(d *model.Device, decoded map[string]any) {
	if decoded == nil {
		return
	}
	now := time.Now().UTC()
	type firing struct {
		rule  model.Rule
		value any
	}
	var fire []firing
	p.rmu.Lock()
	for i := range p.rules {
		r := &p.rules[i]
		if !r.Enabled || r.SourceApp != d.AppID || r.SourceDev != d.ID {
			continue
		}
		raw := decoded[r.Field]
		v, ok := ruleValue(raw)
		if r.Operator != "always" && !ok {
			continue
		}
		if m, _ := CompareRule(r.Operator, v, r.Threshold); !m {
			continue
		}
		if r.LastTriggered != nil && now.Sub(*r.LastTriggered) < time.Duration(r.CooldownSec)*time.Second {
			continue
		}
		t := now
		r.LastTriggered = &t
		fire = append(fire, firing{*r, raw})
	}
	p.rmu.Unlock()
	for _, f := range fire {
		p.fireRule(f.rule, fmt.Sprintf("%s = %v", f.rule.Field, f.value))
	}
}

// FireRule runs a rule's action now, bypassing its condition and cooldown.
func (p *Processor) FireRule(id string) error {
	for _, r := range p.Rules() {
		if r.ID == id {
			return p.fireRule(r, "manual test")
		}
	}
	return fmt.Errorf("rule not found")
}

func (p *Processor) fireRule(r model.Rule, reason string) error {
	payload, _ := hex.DecodeString(r.PayloadHex)
	_, err := p.EnqueueDownlink(r.TargetApp, r.TargetDev, 1, payload, nil, false, false)
	status, name := "queued", "rule.fired"
	summary := fmt.Sprintf("Rule %q (%s): downlink %s for %s", r.Name, reason, r.PayloadHex, r.TargetDev)
	if err != nil {
		status, name = "failed: "+err.Error(), "rule.failed"
		summary = fmt.Sprintf("Rule %q (%s) failed: %v", r.Name, reason, err)
	}
	p.rmu.Lock()
	for i := range p.rules {
		if p.rules[i].ID == r.ID {
			p.rules[i].LastStatus = status
			if p.rules[i].LastTriggered == nil {
				t := time.Now().UTC()
				p.rules[i].LastTriggered = &t
			}
		}
	}
	serr := p.store.SetSetting(settingRules, p.rules)
	p.rmu.Unlock()
	if serr != nil {
		p.log.Warn("save rule state", "err", serr)
	}
	p.emit(&model.Event{Name: name, AppID: r.SourceApp, DevID: r.SourceDev, Summary: summary,
		Data: map[string]any{"rule_id": r.ID, "rule": r.Name, "reason": reason, "target_group": r.TargetApp, "target_node": r.TargetDev,
			"payload_hex": r.PayloadHex, "status": status}}, true)
	if err == nil && r.PulseSec > 0 {
		off, _ := hex.DecodeString(r.PulseOffHex)
		time.AfterFunc(time.Duration(r.PulseSec)*time.Second, func() {
			if _, e := p.EnqueueDownlink(r.TargetApp, r.TargetDev, 1, off, nil, false, false); e != nil {
				p.log.Warn("rule pulse off", "rule", r.ID, "err", e)
			}
		})
	}
	return err
}
