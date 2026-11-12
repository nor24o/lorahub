package processor

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/nor24o/lorahub/internal/model"
)

func name(s string) string {
	return hex.EncodeToString([]byte{byte(len(s))}) + hex.EncodeToString([]byte(s))
}

func uplinkHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDescriptionInTwoParts(t *testing.T) {
	p, st, _, ch := setup(t)
	profileGroup(st, true)
	var mu sync.Mutex
	var changed *model.Device
	p.NodeChanged = func(_, after *model.Device) { mu.Lock(); changed = after; mu.Unlock() }

	// Class A (bits 7-6 00) with the description bit: profile byte 08.
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 08 D1 12 01 11"+name("Relay")), "0000000000000001", 7))
	e := waitEvent(t, ch, "as.node.description_part")
	if !strings.Contains(e.Summary, "part 1 of 2") {
		t.Fatal(e.Summary)
	}
	if d, _ := st.Device("node-cafe", "96b840eb"); d == nil || d.Described != nil || d.Class != "A" {
		t.Fatalf("after part 1: %+v", d)
	}
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 08 D1 22 03 23 000A 0E10 00"+name("Interval")+name("s")), "0000000000000001", 7))
	e = waitEvent(t, ch, "as.node.described")
	if !strings.Contains(e.Summary, "Relay (switch), Interval (number)") {
		t.Fatal(e.Summary)
	}
	d, _ := st.Device("node-cafe", "96b840eb")
	if d.Described == nil || len(d.Described.Commands) != 2 || d.Described.Parts != 2 || len(d.Described.Hex) != 2 {
		t.Fatalf("stored %+v", d.Described)
	}
	if cmds := d.Commands(); len(cmds) != 2 || cmds[0].Kind != "switch" || cmds[1].Kind != "number" {
		t.Fatalf("commands %+v", cmds)
	}
	mu.Lock()
	defer mu.Unlock()
	if changed == nil || changed.Described == nil {
		t.Fatal("Home Assistant was not told about the new commands")
	}
}

func TestDescriptionWithoutProfileOption(t *testing.T) {
	p, st, _, ch := setup(t)
	profileGroup(st, false)
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 88 D1 11 01 11"+name("Relay")), "0000000000000001", 7))
	waitEvent(t, ch, "as.node.described")
	d, _ := st.Device("node-cafe", "96b840eb")
	if d.Described == nil || d.Described.Commands[0].Name != "Relay" || d.Profile != nil {
		t.Fatalf("%+v", d)
	}
	// Ordinary data in the same group is still read as data.
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 0167011002"), "0000000000000001", 7))
	if msg := waitEvent(t, ch, "as.up").Data.(map[string]any); msg["data_hex"] != "0167011002" {
		t.Fatalf("%v", msg)
	}
}

func TestBrokenDescriptionIsReported(t *testing.T) {
	p, st, _, ch := setup(t)
	profileGroup(st, true)
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 88 D1 11 01 11 09 4142"), "0000000000000001", 7))
	e := waitEvent(t, ch, "as.node.description_error")
	if !strings.Contains(e.Summary, "cannot read") {
		t.Fatal(e.Summary)
	}
	if d, _ := st.Device("node-cafe", "96b840eb"); d.Described != nil || d.UplinkCount != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestDescriptionOpensTheReceiveWindow(t *testing.T) {
	p, st, dl, ch := setup(t)
	profileGroup(st, true)
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 00 0167011002"), "0000000000000001", 7)) // class A, registers the node
	waitEvent(t, ch, "as.up")
	if _, err := p.RequestDescription("node-cafe", "96b840eb"); err != nil {
		t.Fatal(err)
	}
	if dl.last() != nil {
		t.Fatal("a class A node got the request before it transmitted")
	}
	p.HandleUplink(rx(uplinkHex(t, "CAFE96B840EB 08 D1 11 02 00"+name("Blink")), "0000000000000001", 7))
	waitEvent(t, ch, "as.node.described")
	waitEvent(t, ch, "ns.down.sent")
	// This group sends no NODE_ID+SERIAL header, so the request is the bare FF.
	if tx := dl.last(); tx == nil || strings.ToUpper(hex.EncodeToString(tx.Payload)) != "FF" || tx.Immediate {
		t.Fatalf("describe request in the receive window: %+v", tx)
	}
}
