package ptyd

// ring is a fixed-capacity byte ring holding the most recent output of a
// session, plus "safe marks": absolute stream offsets right after a
// newline at which the tokenizer was in its ground state. Replay starts at
// the first safe mark still inside the buffer, so a client never receives
// half an escape sequence or half a line.
type ring struct {
	buf   []byte
	total int64 // bytes ever written (absolute offset of the end)
	marks []int64
}

// markSpacing is the minimum distance between recorded safe marks. It
// bounds len(marks) to cap/markSpacing.
const markSpacing = 512

func newRing(capacity int) *ring {
	if capacity < 4096 {
		capacity = 4096
	}
	return &ring{buf: make([]byte, capacity)}
}

// Write appends b, overwriting the oldest data when full.
func (r *ring) Write(b []byte) {
	n := len(r.buf)
	if len(b) >= n {
		copy(r.buf, b[len(b)-n:])
		r.total += int64(len(b))
		// Rotate so that buf[total % n] is the oldest byte.
		off := int(r.total % int64(n))
		rotated := make([]byte, n)
		copy(rotated[off:], r.buf[:n-off])
		copy(rotated[:off], r.buf[n-off:])
		r.buf = rotated
	} else {
		start := int(r.total % int64(n))
		c := copy(r.buf[start:], b)
		copy(r.buf, b[c:])
		r.total += int64(len(b))
	}
	r.prune()
}

// Mark records abs (an offset <= total) as a safe replay start.
func (r *ring) Mark(abs int64) {
	if k := len(r.marks); k > 0 && abs-r.marks[k-1] < markSpacing {
		return
	}
	r.marks = append(r.marks, abs)
}

// Start returns the absolute offset of the oldest byte still buffered.
func (r *ring) Start() int64 {
	if r.total <= int64(len(r.buf)) {
		return 0
	}
	return r.total - int64(len(r.buf))
}

// Truncated reports whether older output has been dropped.
func (r *ring) Truncated() bool { return r.total > int64(len(r.buf)) }

func (r *ring) prune() {
	start := r.Start()
	i := 0
	for i < len(r.marks) && r.marks[i] < start {
		i++
	}
	if i > 0 {
		r.marks = append(r.marks[:0], r.marks[i:]...)
	}
}

// Replay returns a copy of the buffered output starting at a safe
// boundary. When nothing has been dropped the whole buffer is safe.
func (r *ring) Replay() []byte {
	from := r.Start()
	if r.Truncated() {
		if len(r.marks) == 0 {
			return nil
		}
		from = r.marks[0]
	}
	return r.since(from)
}

// since copies the bytes from absolute offset from to the end.
func (r *ring) since(from int64) []byte {
	if from < r.Start() {
		from = r.Start()
	}
	size := int(r.total - from)
	if size <= 0 {
		return nil
	}
	out := make([]byte, size)
	n := len(r.buf)
	start := int(from % int64(n))
	c := copy(out, r.buf[start:])
	if c < size {
		copy(out[c:], r.buf[:size-c])
	}
	return out
}

// Tail returns a new ring holding at most max bytes of the most recent
// output, starting at a safe mark. Used to shrink finished sessions.
func (r *ring) Tail(max int) *ring {
	if r.total-r.Start() <= int64(max) {
		return r
	}
	limit := r.total - int64(max)
	from := int64(-1)
	for _, m := range r.marks {
		if m >= limit {
			from = m
			break
		}
	}
	out := newRing(max)
	if from >= 0 {
		out.Write(r.since(from))
	}
	return out
}
