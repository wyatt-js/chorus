package output

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/http"

	castapp "github.com/vishen/go-chromecast/application"

	"github.com/wyattjs/chorus/internal/audio"
	"github.com/wyattjs/chorus/internal/discover"
)

// Cast streams PCM to a Google Cast device by hosting a live WAV stream over HTTP
// and pointing the device's default media receiver at it.
type Cast struct {
	dev    discover.CastDevice
	format audio.Format
	volume int // 0..100, <0 to leave the device's volume untouched
}

// NewCast builds a Cast output. volume < 0 leaves device volume unchanged.
func NewCast(dev discover.CastDevice, format audio.Format, volume int) *Cast {
	return &Cast{dev: dev, format: format, volume: volume}
}

func (c *Cast) Name() string { return c.dev.Name }

func (c *Cast) Run(ctx context.Context, in <-chan []byte) error {
	// Find the local LAN IP the Cast device can reach us on.
	localIP, err := localIPFor(c.dev.Host)
	if err != nil {
		return fmt.Errorf("cast %s: finding local IP: %w", c.dev.Name, err)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(localIP.String(), "0"))
	if err != nil {
		return fmt.Errorf("cast %s: listen: %w", c.dev.Name, err)
	}
	url := fmt.Sprintf("http://%s/chorus.wav", ln.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/chorus.wav", func(w http.ResponseWriter, r *http.Request) {
		c.serveWAV(ctx, w, in)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	app := castapp.NewApplication()
	if err := app.Start(c.dev.Host.String(), c.dev.Port); err != nil {
		return fmt.Errorf("cast %s: connect: %w", c.dev.Name, err)
	}
	if c.volume >= 0 {
		_ = app.SetVolume(float32(c.volume) / 100)
	}

	// detach + external URL => Load returns immediately and keeps playing.
	if err := app.Load(url, 0, "audio/wav", false, true, true); err != nil {
		return fmt.Errorf("cast %s: load: %w", c.dev.Name, err)
	}
	log.Printf("cast: streaming to %s (%s) via %s", c.dev.Name, c.dev.Host, url)

	<-ctx.Done()
	_ = app.Stop()
	_ = app.Close(true)
	return nil
}

// serveWAV writes a streaming WAV header then copies PCM chunks from in until the
// request context, the run context, or the stream ends.
func (c *Cast) serveWAV(ctx context.Context, w http.ResponseWriter, in <-chan []byte) {
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Connection", "close")
	if _, err := w.Write(wavStreamHeader(c.format)); err != nil {
		return
	}
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-in:
			if !ok {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				return // device disconnected
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// wavStreamHeader builds a 44-byte PCM WAV header with effectively-unbounded
// sizes, suitable for an open-ended live stream.
func wavStreamHeader(f audio.Format) []byte {
	const maxSize = 0x7FFFFFFF
	byteRate := f.SampleRate * f.BytesPerFrame()
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], maxSize)
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(h[20:], 1)  // PCM
	binary.LittleEndian.PutUint16(h[22:], uint16(f.Channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(f.SampleRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(byteRate))
	binary.LittleEndian.PutUint16(h[32:], uint16(f.BytesPerFrame()))
	binary.LittleEndian.PutUint16(h[34:], uint16(f.BitDepth))
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], maxSize)
	return h
}

// localIPFor returns the local source IP used to reach target.
func localIPFor(target net.IP) (net.IP, error) {
	conn, err := net.Dial("udp", net.JoinHostPort(target.String(), "8009"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP, nil
}
