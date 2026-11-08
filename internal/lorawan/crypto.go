// Package lorawan implements the LoRaWAN 1.0.x MAC layer primitives needed
// by a standalone network server: frame parsing, MIC computation, payload
// encryption, join handling and downlink construction.
package lorawan

import (
	"crypto/aes"
	"encoding/binary"
)

// AES128Key is a 16 byte AES key.
type AES128Key [16]byte

// cmac computes AES-CMAC (RFC 4493).
func cmac(key AES128Key, msg []byte) [16]byte {
	block, _ := aes.NewCipher(key[:])
	var zero, l [16]byte
	block.Encrypt(l[:], zero[:])
	k1 := shiftLeft(l)
	if l[0]&0x80 != 0 {
		k1[15] ^= 0x87
	}
	k2 := shiftLeft(k1)
	if k1[0]&0x80 != 0 {
		k2[15] ^= 0x87
	}

	n := (len(msg) + 15) / 16
	complete := n > 0 && len(msg)%16 == 0
	if n == 0 {
		n = 1
	}
	var last [16]byte
	if complete {
		copy(last[:], msg[(n-1)*16:])
		for i := range last {
			last[i] ^= k1[i]
		}
	} else {
		rem := msg[(n-1)*16:]
		copy(last[:], rem)
		last[len(rem)] = 0x80
		for i := range last {
			last[i] ^= k2[i]
		}
	}
	var x, y [16]byte
	for i := 0; i < n-1; i++ {
		for j := 0; j < 16; j++ {
			y[j] = x[j] ^ msg[i*16+j]
		}
		block.Encrypt(x[:], y[:])
	}
	for j := 0; j < 16; j++ {
		y[j] = x[j] ^ last[j]
	}
	block.Encrypt(x[:], y[:])
	return x
}

func shiftLeft(b [16]byte) [16]byte {
	var o [16]byte
	for i := 0; i < 15; i++ {
		o[i] = b[i]<<1 | b[i+1]>>7
	}
	o[15] = b[15] << 1
	return o
}

func blockB(prefix byte, dir byte, devAddr uint32, fcnt uint32, last byte) [16]byte {
	var b [16]byte
	b[0] = prefix
	b[5] = dir
	binary.LittleEndian.PutUint32(b[6:10], devAddr)
	binary.LittleEndian.PutUint32(b[10:14], fcnt)
	b[15] = last
	return b
}

// DataMIC computes the MIC of a data frame (msg = MHDR..FRMPayload).
func DataMIC(nwkSKey AES128Key, dir byte, devAddr uint32, fcnt uint32, msg []byte) [4]byte {
	b0 := blockB(0x49, dir, devAddr, fcnt, byte(len(msg)))
	full := append(b0[:], msg...)
	c := cmac(nwkSKey, full)
	var mic [4]byte
	copy(mic[:], c[:4])
	return mic
}

// EncryptFRMPayload encrypts or decrypts (symmetric) the FRMPayload.
func EncryptFRMPayload(key AES128Key, dir byte, devAddr uint32, fcnt uint32, data []byte) []byte {
	block, _ := aes.NewCipher(key[:])
	out := make([]byte, len(data))
	var s [16]byte
	for i := 0; i < len(data); i += 16 {
		a := blockB(0x01, dir, devAddr, fcnt, byte(i/16+1))
		block.Encrypt(s[:], a[:])
		for j := 0; j < 16 && i+j < len(data); j++ {
			out[i+j] = data[i+j] ^ s[j]
		}
	}
	return out
}

// JoinMIC computes the MIC of a join-request or (unencrypted) join-accept.
func JoinMIC(appKey AES128Key, msg []byte) [4]byte {
	c := cmac(appKey, msg)
	var mic [4]byte
	copy(mic[:], c[:4])
	return mic
}

// DeriveSessionKeys derives NwkSKey and AppSKey for LoRaWAN 1.0.x.
// appNonce and netID are 3 byte little-endian values as sent over the air.
func DeriveSessionKeys(appKey AES128Key, appNonce [3]byte, netID [3]byte, devNonce uint16) (nwk, app AES128Key) {
	block, _ := aes.NewCipher(appKey[:])
	var in [16]byte
	copy(in[1:4], appNonce[:])
	copy(in[4:7], netID[:])
	binary.LittleEndian.PutUint16(in[7:9], devNonce)
	in[0] = 0x01
	block.Encrypt(nwk[:], in[:])
	in[0] = 0x02
	block.Encrypt(app[:], in[:])
	return
}

// encryptJoinAccept applies the AES "decrypt" operation used by LoRaWAN to
// encrypt a join-accept (so the device only needs an AES encrypt primitive).
func encryptJoinAccept(appKey AES128Key, data []byte) []byte {
	block, _ := aes.NewCipher(appKey[:])
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		block.Decrypt(out[i:i+16], data[i:i+16])
	}
	return out
}

// decryptJoinAccept is the device-side operation (used in tests / simulator).
func decryptJoinAccept(appKey AES128Key, data []byte) []byte {
	block, _ := aes.NewCipher(appKey[:])
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		block.Encrypt(out[i:i+16], data[i:i+16])
	}
	return out
}
