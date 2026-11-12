package processor

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

// A node can describe the commands it accepts (model.ParseDescription). The
// parts of a description are collected here until all have arrived.
type descAcc struct {
	parts   int
	got     map[int]descGot
	started time.Time
}

type descGot struct {
	cmds []model.DescribedCommand
	hex  string
}

// descPartsFor is how long the parts of one description may take to arrive.
const descPartsFor = 10 * time.Minute

// description reports whether a node packet is a description and reads it.
// With the group's profile option on, bit 3 of the profile byte marks it;
// without it, a description is still recognised by its exact layout (a
// profile byte with bit 3, D1, and entries that use every byte).
func (p *Processor) description(app *model.Application, hdr *NodeHeader) (part *model.DescriptionPart, err error, is bool) {
	if hdr.Serial == "" {
		return nil, nil, false
	}
	if hdr.Profile != nil {
		if !hdr.Profile.Describe {
			return nil, nil, false
		}
		part, err = model.ParseDescription(hdr.Data)
		return part, err, true
	}
	if app.NodeProfile {
		return nil, nil, false // the profile could not be read; the packet is shown as data
	}
	prof, n, perr := model.ParseNodeProfile(hdr.Data)
	if perr != nil || !prof.Describe || len(hdr.Data) <= n || hdr.Data[n] != model.DescribeFormat {
		return nil, nil, false
	}
	if part, err = model.ParseDescription(hdr.Data[n:]); err != nil {
		return nil, nil, false // not a description after all
	}
	hdr.Data = hdr.Data[n:]
	return part, nil, true
}

// nodeDescribed handles one description packet from node d.
func (p *Processor) nodeDescribed(app *model.Application, d *model.Device, hdr *NodeHeader, fr *Frame, part *model.DescriptionPart, perr error) {
	before := *d
	base := map[string]any{"node_id": hdr.NodeID, "serial": hdr.Serial, "payload_hex": strings.ToUpper(hex.EncodeToString(fr.Payload))}
	who := hdr.NodeID + " / " + hdr.Serial

	var all []model.DescribedCommand
	var hexes []string
	complete := false
	if perr == nil {
		key := d.AppID + "/" + d.ID
		p.descMu.Lock()
		if p.descParts == nil {
			p.descParts = map[string]*descAcc{}
		}
		acc := p.descParts[key]
		if acc == nil || acc.parts != part.Parts || fr.At.Sub(acc.started) > descPartsFor {
			acc = &descAcc{parts: part.Parts, got: map[int]descGot{}, started: fr.At}
			p.descParts[key] = acc
		}
		acc.got[part.Part] = descGot{cmds: part.Commands, hex: strings.ToUpper(hex.EncodeToString(hdr.Data))}
		if len(acc.got) == acc.parts {
			complete = true
			for i := 1; i <= acc.parts; i++ {
				all = append(all, acc.got[i].cmds...)
				hexes = append(hexes, acc.got[i].hex)
			}
			delete(p.descParts, key)
		}
		p.descMu.Unlock()
		if complete {
			codes := map[int]bool{}
			for _, c := range all {
				if codes[c.Code] {
					perr, complete = fmt.Errorf("code %02X is used in two parts", c.Code), false
					break
				}
				codes[c.Code] = true
			}
		}
	}

	updated, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		p.touchDevice(dv, fr, nil)(dv)
		if hdr.Profile != nil && app.NodeProfile {
			dv.Profile, dv.Class = hdr.Profile, hdr.Profile.Class
		}
		if complete {
			dv.Described = &model.NodeDescription{Commands: all, Parts: part.Parts, Received: fr.At.UTC(), Hex: hexes}
		}
		return nil
	})
	if err != nil {
		return
	}
	switch {
	case perr != nil:
		base["error"] = perr.Error()
		p.emit(&model.Event{Name: "as.node.description_error", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: who + " sent a description LoRaHub cannot read: " + perr.Error(), Data: base}, true)
	case complete:
		names := make([]string, len(all))
		for i, c := range all {
			names[i] = c.Name + " (" + c.Kind + ")"
		}
		base["commands"], base["parts"] = all, part.Parts
		p.emit(&model.Event{Name: "as.node.described", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: fmt.Sprintf("%s described its commands: %s", who, strings.Join(names, ", ")), Data: base}, true)
		if p.NodeChanged != nil {
			p.NodeChanged(&before, updated)
		}
	default:
		base["part"], base["parts"] = part.Part, part.Parts
		p.emit(&model.Event{Name: "as.node.description_part", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: fmt.Sprintf("%s sent part %d of %d of its description", who, part.Part, part.Parts), Data: base}, true)
	}
	// A description is an uplink like any other: a class A node listens right after it.
	updated = p.dropExpired(updated)
	if len(updated.Queue) > 0 {
		if cfg := downlinkCfg(app, updated); cfg.Mode == "after_uplink" {
			p.sendRawDown(updated, fr)
		}
	}
}

// RequestDescription asks a node to describe its commands: it queues the
// reserved command FF, framed like any other downlink to the node. A class C
// node gets it at once, a class A node after its next uplink.
func (p *Processor) RequestDescription(appID, devID string) (*model.QueuedDownlink, error) {
	return p.EnqueueDownlink(appID, devID, 1, []byte{model.DescribeRequest}, nil, false, false)
}
