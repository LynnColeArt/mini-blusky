//go:build !llama

package embed

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

type Model struct {
	dims int
	mu   sync.Mutex
	rng  *rand.Rand
}

type Config struct {
	ModelPath     string
	NumThreads    int
	EmbeddingOnly bool
	ContextLength int
}

func DefaultConfig(modelPath string) Config {
	return Config{
		ModelPath:     modelPath,
		NumThreads:    4,
		EmbeddingOnly: true,
		ContextLength: 512,
	}
}

func New(cfg Config) (*Model, error) {
	return &Model{
		dims: 768,
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

func (m *Model) Embed(text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("empty text")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	embedding := make([]float32, m.dims)
	for i := range embedding {
		embedding[i] = m.rng.Float32()*2 - 1
	}

	normalizeInPlace(embedding)
	return embedding, nil
}

func (m *Model) Close() {}

func (m *Model) Dimensions() int {
	return m.dims
}

func normalizeInPlace(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	norm := float32(1.0)
	if sum > 0 {
		norm = 1.0 / sqrt32(sum)
	}
	for i := range v {
		v[i] *= norm
	}
}

func sqrt32(x float32) float32 {
	return float32(sqrt(float64(x)))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}
