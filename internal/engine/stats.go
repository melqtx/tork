package engine

import "time"

// ring is a fixed-size ring buffer of (time, bytes) samples used to smooth
// download speed over a ~5s window instead of instantaneous deltas.
const ringSize = 10

type sample struct {
	at    time.Time
	bytes int64
}

type ring struct {
	buf  [ringSize]sample
	head int // next write position
	n    int // valid samples
}

func (r *ring) push(s sample) {
	r.buf[r.head] = s
	r.head = (r.head + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
}

// speedBps returns bytes/sec across the oldest and newest samples.
func (r *ring) speedBps() float64 {
	if r.n < 2 {
		return 0
	}
	newest := r.buf[(r.head-1+ringSize)%ringSize]
	oldest := r.buf[(r.head-r.n+ringSize)%ringSize]
	dt := newest.at.Sub(oldest.at).Seconds()
	if dt <= 0 {
		return 0
	}
	db := newest.bytes - oldest.bytes
	if db < 0 {
		return 0
	}
	return float64(db) / dt
}
