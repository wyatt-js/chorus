package calibrate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
	"github.com/wyattjs/chorus/internal/output"
)

// Prober plays a calibration tone on one named output and silences the others,
// returning the instant the tone was emitted. *pipeline.Session satisfies it.
type Prober interface {
	Probe(name string, pcm []byte, window time.Duration) (time.Time, error)
}

// MinScore is the matched-filter detection threshold: below it the chirp wasn't
// heard clearly (too far, too quiet, wrong device playing) and Measure errors
// rather than apply a bogus latency.
const MinScore = 6.0

// probeWindow must outlast the slowest output's latency so the chirp is recorded
// (and the others stay silent) until it actually comes out of a high-buffer sink.
// AirPlay and Cast can lag ~8s+, so this is sized well past that; the recording
// runs probeWindow+1s. The cost is that every measurement takes this long, even
// for low-latency Bluetooth — acceptable for a one-time-per-device calibration.
const probeWindow = 13 * time.Second

// Measure plays one chirp on the named output, records the Mac's mic, and
// returns that output's acoustic latency — the full path from "handed to the
// pipeline" to "heard at the mic" (our buffer + network + the device's own
// buffer). Put the mic near the device under test. The result carries a
// constant capture-path bias that cancels when latencies are differenced to set
// per-device offsets, so only the relative values matter.
func Measure(ctx context.Context, p Prober, name string, format audio.Format) (time.Duration, error) {
	chirp := DefaultChirp(format)

	bin, err := output.ChorusAudioPath()
	if err != nil {
		return 0, err
	}
	// Record a little past the probe window so a late, high-latency arrival is
	// still captured.
	recSecs := probeWindow.Seconds() + 1
	cmd := exec.CommandContext(ctx, bin, "record", "--seconds", fmt.Sprintf("%.1f", recSecs))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = output.LogWriter
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting mic recorder: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Block until the first mic byte, then timestamp: that anchors recording
	// sample 0 to our clock (modulo the constant capture bias above).
	br := bufio.NewReader(stdout)
	if _, err := br.Peek(1); err != nil {
		return 0, fmt.Errorf("mic recorder produced no audio: %w", err)
	}
	recStart := time.Now()

	// Drain the rest of the mic stream in the background.
	type result struct {
		samples []float64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		samples, err := readSamples(br)
		done <- result{samples, err}
	}()

	// Let capture settle, then fire the chirp on the device under test.
	time.Sleep(150 * time.Millisecond)
	t0, err := p.Probe(name, chirp.PCM, probeWindow)
	if err != nil {
		return 0, err
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if res.err != nil {
		return 0, res.err
	}

	lag, score := matchedFilter(res.samples, chirp.Reference)
	if score < MinScore {
		return 0, fmt.Errorf("couldn't hear the test tone on %s clearly (score %.1f) — move the Mac closer and retry", name, score)
	}

	// recStart is sample 0's read time, so sample `lag` was heard at recStart +
	// lag/sampleRate; the latency is that arrival minus the emit time t0.
	arrival := recStart.Add(time.Duration(float64(lag) / float64(format.SampleRate) * float64(time.Second)))
	latency := arrival.Sub(t0)
	if latency < 0 {
		latency = 0
	}
	return latency, nil
}

// readSamples decodes an s16le/mono stream to normalized [-1,1] float64 samples
// until EOF.
func readSamples(r io.Reader) ([]float64, error) {
	buf := make([]byte, 8192)
	var out []float64
	for {
		n, err := io.ReadFull(r, buf)
		for i := 0; i+1 < n; i += 2 {
			v := int16(uint16(buf[i]) | uint16(buf[i+1])<<8)
			out = append(out, float64(v)/32768)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}
