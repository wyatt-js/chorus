//go:build airplay

// Package raop is a thin cgo binding over philippe44's libraop RAOP/AirPlay
// client. It exposes just enough of the C API to open a connection to a speaker
// and stream raw PCM frames to it.
//
// The native libraries are built by scripts/build_deps.sh (run `make deps`)
// before this package can compile. The cgo paths below are hardcoded to
// macos/arm64 — the only target Phase 0 supports.
package raop

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/src
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/crosstools/src
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/dmap-parser
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/libmdns/targets/include/mdnssd
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/libmdns/targets/include/mdnssvc
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraop/libopenssl/targets/macos/arm64/include
#cgo CFLAGS: -DNDEBUG -D_GNU_SOURCE -DOPENSSL_SUPPRESS_DEPRECATED -DSSL_STATIC_LIB

#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraop/lib/macos/arm64 -lraop -lcross
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraop/libcodecs/targets/macos/arm64 -lcodecs
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraop/libmdns/targets/macos/arm64 -lmdns
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraop/libopenssl/targets/macos/arm64 -lopenssl
#cgo LDFLAGS: -lc++ -lpthread -ldl -lm

#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include "raop_client.h"
#include "cross_net.h"
#include "cross_ssl.h"
#include "cross_log.h"

// libraop's log-level globals are declared extern by the library objects but
// defined by the application (cliraop normally provides them). Define them here.
log_level util_loglevel = lERROR;
log_level raop_loglevel = lERROR;

// One-time platform init (sockets + OpenSSL), mirroring cliraop's init_platform.
static void at_platform_init(void) {
    netsock_init();
    cross_ssl_load();
}

// Create a client bound to INADDR_ANY (the player address is supplied at
// connect time). frame_len is fixed to DEFAULT_FRAMES_PER_CHUNK (352).
static struct raopcl_s* at_create(int codec, int crypto, int latency_frames,
                                  int sample_rate, int sample_size, int channels,
                                  float volume) {
    struct in_addr host = { INADDR_ANY };
    return raopcl_create(host, 0, 0, NULL, NULL,
                         (raop_codec_t)codec, DEFAULT_FRAMES_PER_CHUNK, latency_frames,
                         (raop_crypto_t)crypto, false, NULL, NULL, NULL, NULL,
                         sample_rate, sample_size, channels, volume);
}

// Connect to the player. ip is a host-order IPv4 address (a<<24|b<<16|c<<8|d).
static bool at_connect(struct raopcl_s* p, uint32_t ip, int port, bool set_volume) {
    struct in_addr addr;
    addr.s_addr = htonl(ip);
    return raopcl_connect(p, addr, (uint16_t)port, set_volume);
}

// Send one chunk of `frames` PCM frames. playtime is required by the API but
// unused here (we don't drive a precise start clock in Phase 0).
static bool at_send_chunk(struct raopcl_s* p, uint8_t* data, int frames) {
    uint64_t playtime;
    return raopcl_send_chunk(p, data, frames, &playtime);
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"unsafe"
)

// FramesPerChunk is libraop's DEFAULT_FRAMES_PER_CHUNK: the number of PCM frames
// passed to SendChunk per call. Callers must feed the stream in this granule.
const FramesPerChunk = 352

// Codec selects the RAOP transport codec.
type Codec int

const (
	CodecPCM  Codec = iota // RAOP_PCM
	CodecALAC              // RAOP_ALAC
)

// Crypto selects the RAOP payload encryption.
type Crypto int

const (
	CryptoClear Crypto = iota // RAOP_CLEAR
	CryptoRSA                 // RAOP_RSA
)

var platformOnce sync.Once

// Config describes a connection to a single AirPlay speaker.
type Config struct {
	Host       net.IP
	Port       int
	Codec      Codec
	Crypto     Crypto
	SampleRate int
	SampleSize int // bits per sample (16)
	Channels   int
	Volume     int // 0..100
}

// Client is a connected RAOP sender. It is not safe for concurrent use; drive it
// from a single goroutine.
type Client struct {
	p *C.struct_raopcl_s
}

// Dial creates a RAOP client and connects to the speaker described by cfg.
func Dial(cfg Config) (*Client, error) {
	platformOnce.Do(func() { C.at_platform_init() })

	ip4 := cfg.Host.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("raop: speaker host %q is not IPv4", cfg.Host)
	}

	// libraop's default requested latency, matching cliraop (1s worth of frames).
	latency := cfg.SampleRate

	p := C.at_create(C.int(cfg.Codec), C.int(cfg.Crypto), C.int(latency),
		C.int(cfg.SampleRate), C.int(cfg.SampleSize), C.int(cfg.Channels),
		C.raopcl_float_volume(C.int(cfg.Volume)))
	if p == nil {
		return nil, fmt.Errorf("raop: raopcl_create failed")
	}

	c := &Client{p: p}
	ip := binary.BigEndian.Uint32(ip4)
	if !bool(C.at_connect(p, C.uint32_t(ip), C.int(cfg.Port), true)) {
		C.raopcl_destroy(p)
		return nil, fmt.Errorf("raop: cannot connect to %s:%d (check firewall/port and that the speaker is free)", cfg.Host, cfg.Port)
	}
	return c, nil
}

// AcceptFrames reports whether the speaker can accept another chunk now. It is
// the pacing mechanism: spin on it before each SendChunk.
func (c *Client) AcceptFrames() bool {
	return bool(C.raopcl_accept_frames(c.p))
}

// SendChunk sends one chunk of PCM. len(pcm) must be a whole number of frames
// (frames = len(pcm) / (channels*sampleSize/8)); pass FramesPerChunk frames.
func (c *Client) SendChunk(pcm []byte, frames int) error {
	if len(pcm) == 0 {
		return nil
	}
	ptr := (*C.uint8_t)(unsafe.Pointer(&pcm[0]))
	if !bool(C.at_send_chunk(c.p, ptr, C.int(frames))) {
		return fmt.Errorf("raop: send_chunk failed")
	}
	return nil
}

// LatencyFrames returns the speaker's reported latency in frames (valid after
// connect).
func (c *Client) LatencyFrames() int {
	return int(C.raopcl_latency(c.p))
}

// SetVolume sets playback volume (0..100).
func (c *Client) SetVolume(vol int) {
	C.raopcl_set_volume(c.p, C.raopcl_float_volume(C.int(vol)))
}

// Close flushes, disconnects and frees the client.
func (c *Client) Close() {
	if c.p == nil {
		return
	}
	C.raopcl_flush(c.p)
	C.raopcl_disconnect(c.p)
	C.raopcl_destroy(c.p)
	c.p = nil
}
