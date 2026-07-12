package engine

import (
	"strings"
	"testing"
	"time"
)

func TestRingSpeed(t *testing.T) {
	var r ring
	base := time.Now()
	if r.speedBps() != 0 {
		t.Error("empty ring should report 0")
	}
	r.push(sample{at: base, bytes: 0})
	if r.speedBps() != 0 {
		t.Error("single sample should report 0")
	}
	// 1 MiB/s over 4 samples
	for i := 1; i <= 3; i++ {
		r.push(sample{at: base.Add(time.Duration(i) * time.Second), bytes: int64(i) << 20})
	}
	got := r.speedBps()
	want := float64(1 << 20)
	if got < want*0.99 || got > want*1.01 {
		t.Errorf("speed = %f, want ~%f", got, want)
	}
}

func TestMetadataStatusSummaryIsTruthful(t *testing.T) {
	direct := MetadataStatus{PeersTotal: 3, Trackers: 16, DHTEnabled: true, Waiting: 12 * time.Second}
	got := direct.Summary()
	for _, want := range []string{"3 peers seen", "16 trackers", "DHT", "12s"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q omitted %q", got, want)
		}
	}
	strict := MetadataStatus{Trackers: 1, ProxyStrict: true}
	if got := strict.Summary(); !strings.Contains(got, "1 TCP tracker") || !strings.Contains(got, "DHT off") {
		t.Fatalf("strict summary = %q", got)
	}
}

func TestRingWrapsAndUsesWindow(t *testing.T) {
	var r ring
	base := time.Now()
	// fill beyond capacity: 20 samples at 2 MiB/s
	for i := 0; i < 20; i++ {
		r.push(sample{at: base.Add(time.Duration(i) * time.Second), bytes: int64(i) * 2 << 20})
	}
	got := r.speedBps()
	want := float64(2 << 20)
	if got < want*0.99 || got > want*1.01 {
		t.Errorf("speed = %f, want ~%f", got, want)
	}
	if r.n != ringSize {
		t.Errorf("n = %d, want %d", r.n, ringSize)
	}
}

func TestRingZeroDeltaAndRegression(t *testing.T) {
	var r ring
	base := time.Now()
	r.push(sample{at: base, bytes: 100})
	r.push(sample{at: base, bytes: 100}) // same timestamp
	if r.speedBps() != 0 {
		t.Error("zero time delta should report 0")
	}
	var r2 ring
	r2.push(sample{at: base, bytes: 200})
	r2.push(sample{at: base.Add(time.Second), bytes: 100}) // bytes went down
	if r2.speedBps() != 0 {
		t.Error("negative byte delta should report 0")
	}
}
