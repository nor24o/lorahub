package processor

import (
	"encoding/hex"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

type fakeDL struct {
	mu   sync.Mutex
	sent []model.TxPacket
}

func (f *fakeDL) CanSend(string) bool { return true }
func (f *fakeDL) Send(_ string, tx model.TxPacket) error {
	f.mu.Lock()
	f.sent = append(f.sent, tx)
	f.mu.Unlock()
	return nil
}
func (f *fakeDL) last() *model.TxPacket {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return nil
	}
	return &f.sent[len(f.sent)-1]
}

func setup(t *testing.T) (*Processor, *store.Store, *fakeDL, <-chan *model.Event) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.New()
	p := New(st, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := p.Settings()
	s.DedupWindowMs = 50
	p.SetSettings(s)
	dl := &fakeDL{}
	p.RegisterDownlinker("semtech_udp", dl)
	ch, _ := bus.Subscribe(256, nil)
	st.SaveApplication(&model.Application{ID: "app", Uplink: model.Formatter{Type: "cayenne_lpp"}})
	return p, st, dl, ch
}

func rx(payload []byte, eui string, snr float64) model.RxPacket {
	return model.RxPacket{Payload: payload, GatewayEUI: eui,
		Settings: model.LoRaSettings{Modulation: "LORA", SF: 7, BW: 125, CodingRate: "4/5", Frequency: 868100000, DataRate: "SF7BW125"},
		Meta:     model.RxMetadata{Source: "semtech_udp", Timestamp: 1000000, RSSI: -80, SNR: snr, CRCStatus: "ok", ReceivedAt: time.Now()}}
}

func waitEvent(t *testing.T, ch <-chan *model.Event, name string) *model.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Name == name {
				return e
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %s", name)
		}
	}
}

func TestOTAAJoinAndDedupUplink(t *testing.T) {
	p, st, dl, ch := setup(t)
	appKey, _ := lorawan.ParseKey("000102030405060708090A0B0C0D0E0F")
	st.SaveDevice(&model.Device{ID: "dev", AppID: "app", Mode: "otaa", DevEUI: "0004A30B001C0530", JoinEUI: "70B3D57ED0000001",
		AppKey: "000102030405060708090A0B0C0D0E0F", Band: "EU868"})
	de, _ := lorawan.ParseEUI("0004A30B001C0530")
	je, _ := lorawan.ParseEUI("70B3D57ED0000001")

	p.HandleUplink(rx(lorawan.BuildJoinRequest(je, de, 42, appKey), "0000000000000001", 5))
	waitEvent(t, ch, "ns.join.accept")
	tx := dl.last()
	if tx == nil || tx.Timestamp != 6000000 || !tx.InvertPol {
		t.Fatalf("join accept not scheduled correctly: %+v", tx)
	}
	ja, ok := lorawan.DecodeJoinAccept(tx.Payload, appKey)
	if !ok {
		t.Fatal("join accept MIC invalid")
	}
	nwk, app := lorawan.DeriveSessionKeys(appKey, ja.AppNonce, ja.NetID, 42)

	// Same uplink heard by two gateways -> one as.up with 2 rx_metadata.
	payload, _ := hex.DecodeString("0367011002687F")
	up := lorawan.BuildDataUp(true, ja.DevAddr, 0, 1, payload, nwk, app)
	p.HandleUplink(rx(up, "0000000000000001", 2))
	p.HandleUplink(rx(up, "0000000000000002", 9))
	e := waitEvent(t, ch, "as.up")
	msg := e.Data.(map[string]any)["uplink_message"].(map[string]any)
	meta := msg["rx_metadata"].([]model.RxMetadata)
	if len(meta) != 2 || meta[0].GatewayIDs.EUI != "0000000000000002" {
		t.Fatalf("expected 2 gateways sorted by SNR, got %+v", meta)
	}
	dec := msg["decoded_payload"].(map[string]any)
	if dec["temperature_3"].(float64) != 27.2 {
		t.Fatalf("decoded %v", dec)
	}
	// confirmed uplink -> ACK downlink via best gateway
	waitEvent(t, ch, "ns.down.sent")
	ack, _ := lorawan.Parse(dl.last().Payload)
	if !ack.ACK || !ack.VerifyDataMIC(nwk, 0) {
		t.Fatal("bad ACK downlink")
	}
	// replay is rejected
	p.HandleUplink(rx(lorawan.BuildDataUp(false, ja.DevAddr, 0, 1, payload, nwk, app), "0000000000000001", 2))
	waitEvent(t, ch, "ns.up.retransmission")
}

func TestRawDevice(t *testing.T) {
	p, st, _, ch := setup(t)
	st.SaveDevice(&model.Device{ID: "cafe", AppID: "app", Mode: "raw", RawMatch: &model.RawMatch{Type: "prefix", Value: "CAFE"},
		Uplink: model.Formatter{Type: "fields", Fields: []model.Field{{Name: "v", Offset: 2, Type: "uint16_be", Scale: 0.1}}}})
	st.SaveDevice(&model.Device{ID: "cafe01", AppID: "app", Mode: "raw", RawMatch: &model.RawMatch{Type: "prefix", Value: "CAFE01"}})
	p.HandleUplink(rx([]byte{0xCA, 0xFE, 0x00, 0xFA}, "0000000000000001", 3))
	e := waitEvent(t, ch, "as.up")
	if e.DevID != "cafe" {
		t.Fatalf("matched %s", e.DevID)
	}
	p.HandleUplink(rx([]byte{0xCA, 0xFE, 0x01, 0x02}, "0000000000000001", 3))
	if e := waitEvent(t, ch, "as.up"); e.DevID != "cafe01" {
		t.Fatalf("longest match should win, got %s", e.DevID)
	}
	p.HandleUplink(rx([]byte{0x01, 0x02, 0x03}, "0000000000000001", 3))
	waitEvent(t, ch, "ns.up.unmatched")
}
