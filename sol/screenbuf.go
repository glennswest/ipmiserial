package sol

import "sync"

const defaultScreenBufSize = 64 * 1024 // 64KB

// ScreenBuffer maintains a rolling buffer of raw SOL bytes.
// Used for terminal catchup when switching between servers —
// replaying raw bytes into xterm.js produces correct screen state.
type ScreenBuffer struct {
	mu   sync.RWMutex
	data []byte
	max  int
}

func NewScreenBuffer(maxSize int) *ScreenBuffer {
	return &ScreenBuffer{
		data: make([]byte, 0, maxSize),
		max:  maxSize,
	}
}

func (sb *ScreenBuffer) Write(p []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.data = append(sb.data, p...)
	if len(sb.data) > sb.max {
		excess := len(sb.data) - sb.max
		copy(sb.data, sb.data[excess:])
		sb.data = sb.data[:sb.max]
	}
}

func (sb *ScreenBuffer) Bytes() []byte {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	out := make([]byte, len(sb.data))
	copy(out, sb.data)
	return out
}

func (sb *ScreenBuffer) Reset() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.data = sb.data[:0]
}
