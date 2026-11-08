// Command lorahub-sim simulates a Semtech UDP gateway with a LoRaWAN OTAA
// device (or a raw payload sender) to test a LoRaHub installation end to
// end without radio hardware.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/nor24o/lorahub/internal/lorawan"
)

func main() {
	server := flag.String("server", "127.0.0.1:1700", "LoRaHub Semtech UDP address")
	gwEUI := flag.String("gateway-eui", "AA555A0000000101", "simulated gateway EUI")
	mode := flag.String("mode", "raw", "raw (NODE_ID|SERIAL|data packets) | otaa (LoRaWAN test)")
	devEUI := flag.String("dev-eui", "0004A30B001C0530", "device EUI")
	joinEUI := flag.String("join-eui", "70B3D57ED0000001", "join EUI")
	appKeyS := flag.String("app-key", "000102030405060708090A0B0C0D0E0F", "AppKey")
	payloadHex := flag.String("payload", "CAFE96B840EB0102014A0367010F", "payload hex (default: NODE_ID CAFE, serial 96B840EB, Cayenne data)")
	count := flag.Int("count", 3, "number of uplinks")
	interval := flag.Duration("interval", 3*time.Second, "uplink interval")
	confirmed := flag.Bool("confirmed", false, "send confirmed uplinks")
	freq := flag.Float64("freq", 868.1, "frequency MHz")
	flag.Parse()

	ua, err := net.ResolveUDPAddr("udp", *server)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		log.Fatal(err)
	}
	eui, _ := lorawan.ParseEUI(*gwEUI)
	start := time.Now()
	tmst := func() uint32 { return uint32(time.Since(start).Microseconds()) }

	header := func(typ byte) []byte {
		h := []byte{2, byte(rand.Intn(256)), byte(rand.Intn(256)), typ}
		return append(h, eui[:]...)
	}
	down := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if n >= 4 && buf[3] == 0x03 {
				var b struct {
					Txpk map[string]any `json:"txpk"`
				}
				json.Unmarshal(buf[4:n], &b)
				data, _ := base64.StdEncoding.DecodeString(fmt.Sprint(b.Txpk["data"]))
				log.Printf("<- PULL_RESP txpk freq=%v datr=%v tmst=%v powe=%v ipol=%v payload=%X", b.Txpk["freq"], b.Txpk["datr"], b.Txpk["tmst"], b.Txpk["powe"], b.Txpk["ipol"], data)
				ack := header(0x05)
				ack[1], ack[2] = buf[1], buf[2]
				conn.Write(ack)
				down <- data
			}
		}
	}()
	conn.Write(header(0x02)) // PULL_DATA
	go func() {
		for range time.Tick(5 * time.Second) {
			conn.Write(header(0x02))
		}
	}()

	push := func(phy []byte) {
		rx := map[string]any{"tmst": tmst(), "time": time.Now().UTC().Format(time.RFC3339Nano), "chan": 0, "rfch": 0, "freq": *freq, "stat": 1,
			"modu": "LORA", "datr": "SF7BW125", "codr": "4/5", "rssi": -57 - rand.Intn(20), "lsnr": 9.5, "size": len(phy),
			"data": base64.StdEncoding.EncodeToString(phy)}
		body, _ := json.Marshal(map[string]any{"rxpk": []any{rx},
			"stat": map[string]any{"time": time.Now().UTC().Format("2006-01-02 15:04:05 MST"), "rxnb": 1, "rxok": 1, "rxfw": 1, "ackr": 100, "dwnb": 0, "txnb": 0}})
		conn.Write(append(header(0x00), body...))
		log.Printf("-> PUSH_DATA %d bytes %X", len(phy), phy)
	}
	time.Sleep(300 * time.Millisecond)
	payload, err := hex.DecodeString(*payloadHex)
	if err != nil {
		log.Fatal("payload: ", err)
	}

	if *mode == "raw" {
		for i := 0; i < *count; i++ {
			push(payload)
			time.Sleep(*interval)
		}
		return
	}

	appKey, err := lorawan.ParseKey(*appKeyS)
	if err != nil {
		log.Fatal(err)
	}
	de, _ := lorawan.ParseEUI(*devEUI)
	je, _ := lorawan.ParseEUI(*joinEUI)
	nonce := uint16(rand.Intn(65535))
	push(lorawan.BuildJoinRequest(je, de, nonce, appKey))
	var ja []byte
	select {
	case ja = <-down:
	case <-time.After(10 * time.Second):
		log.Println("no join accept received")
		os.Exit(1)
	}
	p, ok := lorawan.DecodeJoinAccept(ja, appKey)
	if !ok {
		log.Fatal("join accept MIC invalid")
	}
	nwk, app := lorawan.DeriveSessionKeys(appKey, p.AppNonce, p.NetID, nonce)
	log.Printf("joined: DevAddr=%08X NwkSKey=%X AppSKey=%X", p.DevAddr, nwk, app)

	for i := 0; i < *count; i++ {
		push(lorawan.BuildDataUp(*confirmed, p.DevAddr, uint32(i), 1, payload, nwk, app))
		select {
		case d := <-down:
			f, err := lorawan.Parse(d)
			if err == nil {
				fc := uint32(f.FCnt16)
				dec := f.Decrypt(nwk, app, fc)
				log.Printf("downlink: ack=%v fcnt=%d mic_ok=%v fport=%v payload=%X fopts=%X", f.ACK, fc, f.VerifyDataMIC(nwk, fc), portStr(f.FPort), dec, f.FOpts)
			}
		case <-time.After(*interval):
		}
	}
	_ = binary.LittleEndian
}

func portStr(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}
