package lorawan

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// MType values.
const (
	JoinRequest         = 0
	JoinAccept          = 1
	UnconfirmedDataUp   = 2
	UnconfirmedDataDown = 3
	ConfirmedDataUp     = 4
	ConfirmedDataDown   = 5
	RejoinRequest       = 6
	Proprietary         = 7
)

// MTypeName returns a human readable message type.
func MTypeName(m int) string {
	return [...]string{"JoinRequest", "JoinAccept", "UnconfirmedDataUp", "UnconfirmedDataDown",
		"ConfirmedDataUp", "ConfirmedDataDown", "RejoinRequest", "Proprietary"}[m&7]
}

// EUI64 is an 8 byte identifier stored in big-endian (display) order.
type EUI64 [8]byte

func (e EUI64) String() string { return strings.ToUpper(hex.EncodeToString(e[:])) }

// ParseEUI parses a 16 hex char EUI (separators allowed).
func ParseEUI(s string) (EUI64, error) {
	var e EUI64
	b, err := hex.DecodeString(cleanHex(s))
	if err != nil || len(b) != 8 {
		return e, fmt.Errorf("invalid EUI %q", s)
	}
	copy(e[:], b)
	return e, nil
}

// ParseKey parses a 32 hex char AES key.
func ParseKey(s string) (AES128Key, error) {
	var k AES128Key
	b, err := hex.DecodeString(cleanHex(s))
	if err != nil || len(b) != 16 {
		return k, fmt.Errorf("invalid AES-128 key")
	}
	copy(k[:], b)
	return k, nil
}

// ParseDevAddr parses an 8 hex char DevAddr.
func ParseDevAddr(s string) (uint32, error) {
	b, err := hex.DecodeString(cleanHex(s))
	if err != nil || len(b) != 4 {
		return 0, fmt.Errorf("invalid DevAddr %q", s)
	}
	return binary.BigEndian.Uint32(b), nil
}

// DevAddrString formats a DevAddr as hex.
func DevAddrString(a uint32) string { return fmt.Sprintf("%08X", a) }

func cleanHex(s string) string {
	r := strings.NewReplacer(" ", "", ":", "", "-", "", "0x", "", "0X", "")
	return r.Replace(strings.TrimSpace(s))
}

func reverse(b []byte) []byte {
	o := make([]byte, len(b))
	for i := range b {
		o[len(b)-1-i] = b[i]
	}
	return o
}

// Frame is a parsed (not yet authenticated) PHYPayload.
type Frame struct {
	Raw   []byte
	MHDR  byte
	MType int
	Major int

	// Data frames
	DevAddr    uint32
	FCtrl      byte
	ADR        bool
	ACK        bool
	FPending   bool
	FCnt16     uint16
	FOpts      []byte
	FPort      *int
	FRMPayload []byte

	// Join request
	JoinEUI  EUI64
	DevEUI   EUI64
	DevNonce uint16

	MIC [4]byte
}

// ErrNotLoRaWAN is returned when the payload clearly isn't a LoRaWAN frame.
var ErrNotLoRaWAN = errors.New("not a LoRaWAN frame")

// Parse parses a PHYPayload. It performs structural checks only.
func Parse(b []byte) (*Frame, error) {
	if len(b) < 5 {
		return nil, ErrNotLoRaWAN
	}
	f := &Frame{Raw: b, MHDR: b[0], MType: int(b[0] >> 5), Major: int(b[0] & 0x03)}
	if f.Major != 0 || b[0]&0x1C != 0 {
		return nil, ErrNotLoRaWAN
	}
	copy(f.MIC[:], b[len(b)-4:])
	switch f.MType {
	case JoinRequest:
		if len(b) != 23 {
			return nil, ErrNotLoRaWAN
		}
		copy(f.JoinEUI[:], reverse(b[1:9]))
		copy(f.DevEUI[:], reverse(b[9:17]))
		f.DevNonce = binary.LittleEndian.Uint16(b[17:19])
		return f, nil
	case UnconfirmedDataUp, ConfirmedDataUp, UnconfirmedDataDown, ConfirmedDataDown:
		if len(b) < 12 {
			return nil, ErrNotLoRaWAN
		}
		f.DevAddr = binary.LittleEndian.Uint32(b[1:5])
		f.FCtrl = b[5]
		f.ADR = f.FCtrl&0x80 != 0
		f.ACK = f.FCtrl&0x20 != 0
		f.FPending = f.FCtrl&0x10 != 0
		foptsLen := int(f.FCtrl & 0x0F)
		f.FCnt16 = binary.LittleEndian.Uint16(b[6:8])
		end := len(b) - 4
		if 8+foptsLen > end {
			return nil, ErrNotLoRaWAN
		}
		f.FOpts = b[8 : 8+foptsLen]
		rest := b[8+foptsLen : end]
		if len(rest) > 0 {
			p := int(rest[0])
			f.FPort = &p
			f.FRMPayload = rest[1:]
		}
		return f, nil
	default:
		return nil, ErrNotLoRaWAN
	}
}

// IsUplink reports whether the frame is an uplink type.
func (f *Frame) IsUplink() bool {
	return f.MType == JoinRequest || f.MType == UnconfirmedDataUp || f.MType == ConfirmedDataUp
}

// FullFCnt reconstructs the 32 bit frame counter from the 16 bit value and
// the last known counter.
func FullFCnt(last uint32, fcnt16 uint16) uint32 {
	c := (last & 0xFFFF0000) | uint32(fcnt16)
	if c < last && last-c > 0x8000 {
		c += 0x10000
	}
	return c
}

// VerifyDataMIC checks the MIC of a data uplink with the given 32 bit FCnt.
func (f *Frame) VerifyDataMIC(nwkSKey AES128Key, fcnt uint32) bool {
	dir := byte(0)
	if !f.IsUplink() {
		dir = 1
	}
	mic := DataMIC(nwkSKey, dir, f.DevAddr, fcnt, f.Raw[:len(f.Raw)-4])
	return mic == f.MIC
}

// VerifyJoinMIC checks the MIC of a join request.
func (f *Frame) VerifyJoinMIC(appKey AES128Key) bool {
	return JoinMIC(appKey, f.Raw[:len(f.Raw)-4]) == f.MIC
}

// Decrypt returns the decrypted FRMPayload.
func (f *Frame) Decrypt(nwkSKey, appSKey AES128Key, fcnt uint32) []byte {
	if f.FPort == nil {
		return nil
	}
	key := appSKey
	if *f.FPort == 0 {
		key = nwkSKey
	}
	dir := byte(0)
	if !f.IsUplink() {
		dir = 1
	}
	return EncryptFRMPayload(key, dir, f.DevAddr, fcnt, f.FRMPayload)
}

// DataDown describes a downlink data frame to build.
type DataDown struct {
	Confirmed bool
	DevAddr   uint32
	ACK       bool
	FPending  bool
	ADR       bool
	FCnt      uint32
	FOpts     []byte
	FPort     *int
	Payload   []byte // plaintext
}

// BuildDataDown builds and encrypts a downlink PHYPayload.
func BuildDataDown(d DataDown, nwkSKey, appSKey AES128Key) []byte {
	mt := UnconfirmedDataDown
	if d.Confirmed {
		mt = ConfirmedDataDown
	}
	b := []byte{byte(mt << 5)}
	var addr [4]byte
	binary.LittleEndian.PutUint32(addr[:], d.DevAddr)
	b = append(b, addr[:]...)
	fctrl := byte(len(d.FOpts) & 0x0F)
	if d.ADR {
		fctrl |= 0x80
	}
	if d.ACK {
		fctrl |= 0x20
	}
	if d.FPending {
		fctrl |= 0x10
	}
	b = append(b, fctrl, byte(d.FCnt), byte(d.FCnt>>8))
	b = append(b, d.FOpts...)
	if d.FPort != nil {
		b = append(b, byte(*d.FPort))
		key := appSKey
		if *d.FPort == 0 {
			key = nwkSKey
		}
		b = append(b, EncryptFRMPayload(key, 1, d.DevAddr, d.FCnt, d.Payload)...)
	}
	mic := DataMIC(nwkSKey, 1, d.DevAddr, d.FCnt, b)
	return append(b, mic[:]...)
}

// BuildDataUp builds an uplink frame (used by the simulator and tests).
func BuildDataUp(confirmed bool, devAddr uint32, fcnt uint32, fport int, payload []byte, nwkSKey, appSKey AES128Key) []byte {
	mt := UnconfirmedDataUp
	if confirmed {
		mt = ConfirmedDataUp
	}
	b := []byte{byte(mt << 5)}
	var addr [4]byte
	binary.LittleEndian.PutUint32(addr[:], devAddr)
	b = append(b, addr[:]...)
	b = append(b, 0x00, byte(fcnt), byte(fcnt>>8), byte(fport))
	key := appSKey
	if fport == 0 {
		key = nwkSKey
	}
	b = append(b, EncryptFRMPayload(key, 0, devAddr, fcnt, payload)...)
	mic := DataMIC(nwkSKey, 0, devAddr, fcnt, b)
	return append(b, mic[:]...)
}

// BuildJoinRequest builds a join request (simulator / tests).
func BuildJoinRequest(joinEUI, devEUI EUI64, devNonce uint16, appKey AES128Key) []byte {
	b := []byte{0x00}
	b = append(b, reverse(joinEUI[:])...)
	b = append(b, reverse(devEUI[:])...)
	b = append(b, byte(devNonce), byte(devNonce>>8))
	mic := JoinMIC(appKey, b)
	return append(b, mic[:]...)
}

// JoinAcceptParams holds values for a join accept.
type JoinAcceptParams struct {
	AppNonce [3]byte
	NetID    [3]byte
	DevAddr  uint32
	RX1DROff uint8
	RX2DR    uint8
	RxDelay  uint8
	CFList   []byte // optional 16 bytes
}

// BuildJoinAccept builds the encrypted join-accept PHYPayload.
func BuildJoinAccept(p JoinAcceptParams, appKey AES128Key) []byte {
	b := []byte{0x20}
	b = append(b, p.AppNonce[:]...)
	b = append(b, p.NetID[:]...)
	var addr [4]byte
	binary.LittleEndian.PutUint32(addr[:], p.DevAddr)
	b = append(b, addr[:]...)
	b = append(b, (p.RX1DROff&0x07)<<4|(p.RX2DR&0x0F), p.RxDelay&0x0F)
	if len(p.CFList) == 16 {
		b = append(b, p.CFList...)
	}
	mic := JoinMIC(appKey, b)
	b = append(b, mic[:]...)
	enc := encryptJoinAccept(appKey, b[1:])
	return append([]byte{b[0]}, enc...)
}

// DecodeJoinAccept decrypts a join-accept (device side; for tests).
func DecodeJoinAccept(phy []byte, appKey AES128Key) (JoinAcceptParams, bool) {
	var p JoinAcceptParams
	if len(phy) != 17 && len(phy) != 33 {
		return p, false
	}
	dec := append([]byte{phy[0]}, decryptJoinAccept(appKey, phy[1:])...)
	mic := JoinMIC(appKey, dec[:len(dec)-4])
	ok := string(mic[:]) == string(dec[len(dec)-4:])
	copy(p.AppNonce[:], dec[1:4])
	copy(p.NetID[:], dec[4:7])
	p.DevAddr = binary.LittleEndian.Uint32(dec[7:11])
	p.RX1DROff = (dec[11] >> 4) & 0x07
	p.RX2DR = dec[11] & 0x0F
	p.RxDelay = dec[12]
	return p, ok
}

// RandomBytes fills n random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
