//go:build cgo && llama

package embed

/*
#cgo CFLAGS: -I${SRCDIR}/../../llama/include
#cgo LDFLAGS: -L${SRCDIR}/../../llama/lib -lllama -lggml -lggml-cpu -lm -lpthread -lstdc++

#include <stdlib.h>
#include "llama.h"

static llama_model* load_model(const char* path, int n_threads) {
    llama_model_params params = llama_model_default_params();
    params.n_gpu_layers = 0;
    return llama_load_model_from_file(path, params);
}

static llama_context* create_context(llama_model* model, int n_ctx, int n_threads) {
    llama_context_params params = llama_context_default_params();
    params.n_ctx = n_ctx;
    params.n_threads = n_threads;
    params.n_threads_batch = n_threads;
    params.embeddings = true;
    return llama_new_context_with_model(model, params);
}

static int tokenize(llama_context* ctx, const char* text, llama_token* tokens, int n_max_tokens) {
    return llama_tokenize(ctx, text, (int32_t)strlen(text), tokens, n_max_tokens, true, true);
}

static float* get_embeddings(llama_context* ctx) {
    return llama_get_embeddings_seq(ctx, 0);
}

static int get_n_embd(llama_context* ctx) {
    return llama_model_n_embd(llama_get_model(ctx));
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type Model struct {
	model *C.llama_model
	ctx   *C.llama_context
	dims  int
	mu    sync.Mutex
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
	if cfg.ModelPath == "" {
		return nil, errors.New("model path required")
	}

	C.llama_backend_init()

	cPath := C.CString(cfg.ModelPath)
	defer C.free(unsafe.Pointer(cPath))

	model := C.load_model(cPath, C.int(cfg.NumThreads))
	if model == nil {
		return nil, fmt.Errorf("failed to load model: %s", cfg.ModelPath)
	}

	ctx := C.create_context(model, C.int(cfg.ContextLength), C.int(cfg.NumThreads))
	if ctx == nil {
		C.llama_free_model(model)
		return nil, errors.New("failed to create context")
	}

	nEmb := C.get_n_embd(ctx)

	return &Model{
		model: model,
		ctx:   ctx,
		dims:  int(nEmb),
	}, nil
}

func (m *Model) Embed(text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("empty text")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	tokens := make([]C.llama_token, C.llama_n_ctx(m.ctx))
	nTokens := C.tokenize(m.ctx, cText, &tokens[0], C.int(len(tokens)))
	if nTokens <= 0 {
		return nil, errors.New("tokenization failed")
	}

	batch := C.llama_batch_get_one(&tokens[0], nTokens)

	ret := C.llama_decode(m.ctx, batch)
	if ret < 0 {
		return nil, fmt.Errorf("llama_decode failed: %d", ret)
	}

	embeddings := C.get_embeddings(m.ctx)
	if embeddings == nil {
		return nil, errors.New("failed to get embeddings")
	}

	result := make([]float32, m.dims)
	for i := 0; i < m.dims; i++ {
		result[i] = float32(*(*C.float)(unsafe.Pointer(uintptr(unsafe.Pointer(embeddings)) + uintptr(i)*unsafe.Sizeof(C.float(0)))))
	}

	C.llama_kv_cache_clear(m.ctx)

	normalizeInPlace(result)
	return result, nil
}

func (m *Model) Close() {
	if m.ctx != nil {
		C.llama_free(m.ctx)
		m.ctx = nil
	}
	if m.model != nil {
		C.llama_free_model(m.model)
		m.model = nil
	}
}

func (m *Model) Dimensions() int {
	return m.dims
}

func normalizeInPlace(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum > 0 {
		norm := 1.0 / sqrt32(sum)
		for i := range v {
			v[i] *= norm
		}
	}
}

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}
