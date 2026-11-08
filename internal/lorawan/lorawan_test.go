package lorawan

import (
	"encoding/hex"
	"testing"
)

func TestCMACRFC4493(t *testing.T) {
	key, _ := ParseKey("2b7e151628aed2a6abf7158809cf4f3c")
	cases := map[string]string{
		"":                                 "bb1d6929e95937287fa37d129b756746",
		"6bc1bee22e409f96e93d7e117393172a": "070a16b46b4d4144f79bdd9dd04a287c",
		"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411": "dfa66747de9ae63030ca32611497c827",
	}
	for in, want := range cases {
		m, _ := hex.DecodeString(in)
		got := cmac(key, m)
		if hex.EncodeToString(got[:]) != want {
			t.Fatalf("cmac(%s)=%x want %s", in, got, want)
		}
	}
}

func TestKnownUplink(t *testing.T) {
	raw, _ := hex.DecodeString("40F17DBE4900020001954378762B11FF0D")
	nwk, _ := ParseKey("44024241ed4ce9a68c6a8bc055233fd3")
	app, _ := ParseKey("ec925802ae430ca77fd3dd73cb2cc588")
	f, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if DevAddrString(f.DevAddr) != "49BE7DF1" || f.FCnt16 != 2 {
		t.Fatalf("bad header %08X %d", f.DevAddr, f.FCnt16)
	}
	if !f.VerifyDataMIC(nwk, 2) {
		t.Fatal("mic mismatch")
	}
	if string(f.Decrypt(nwk, app, 2)) != "test" {
		t.Fatalf("decrypt got %q", f.Decrypt(nwk, app, 2))
	}
}

func TestJoinRoundTrip(t *testing.T) {
	appKey, _ := ParseKey("000102030405060708090A0B0C0D0E0F")
	je, _ := ParseEUI("70B3D57ED0000001")
	de, _ := ParseEUI("0004A30B001C0530")
	jr := BuildJoinRequest(je, de, 0x1234, appKey)
	f, err := Parse(jr)
	if err != nil || f.DevEUI != de || f.JoinEUI != je || f.DevNonce != 0x1234 || !f.VerifyJoinMIC(appKey) {
		t.Fatalf("join request parse failed %v", err)
	}
	p := JoinAcceptParams{AppNonce: [3]byte{1, 2, 3}, NetID: [3]byte{0x13}, DevAddr: 0x26011234, RxDelay: 1}
	ja := BuildJoinAccept(p, appKey)
	got, ok := DecodeJoinAccept(ja, appKey)
	if !ok || got.DevAddr != p.DevAddr || got.AppNonce != p.AppNonce {
		t.Fatalf("join accept roundtrip failed %+v ok=%v", got, ok)
	}
}

func TestDataRoundTrip(t *testing.T) {
	nwk, _ := ParseKey("44024241ed4ce9a68c6a8bc055233fd3")
	app, _ := ParseKey("ec925802ae430ca77fd3dd73cb2cc588")
	up := BuildDataUp(true, 0x26010203, 70000, 5, []byte("hello world, this is long"), nwk, app)
	f, _ := Parse(up)
	fc := FullFCnt(69990, f.FCnt16)
	if fc != 70000 || !f.VerifyDataMIC(nwk, fc) || string(f.Decrypt(nwk, app, fc)) != "hello world, this is long" {
		t.Fatal("roundtrip failed")
	}
	p := 3
	dn := BuildDataDown(DataDown{DevAddr: 0x26010203, ACK: true, FCnt: 7, FPort: &p, Payload: []byte{1, 2}}, nwk, app)
	g, _ := Parse(dn)
	if !g.ACK || !g.VerifyDataMIC(nwk, 7) || hex.EncodeToString(g.Decrypt(nwk, app, 7)) != "0102" {
		t.Fatal("downlink roundtrip failed")
	}
}
