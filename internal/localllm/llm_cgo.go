//go:build localllm

package localllm

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/llama/include -std=c11
#cgo linux LDFLAGS: -L${SRCDIR}/../../third_party/llama/lib -lllama -lggml -lggml-cpu -lstdc++ -lm -lgomp
#cgo darwin LDFLAGS: -L${SRCDIR}/../../third_party/llama/lib -lllama -lggml -lggml-cpu -lstdc++ -lm
#include "llama.h"
#include <stdbool.h>
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"fmt"
	"unsafe"
)

type llamaEngine struct {
	model *C.struct_llama_model
	ctx   *C.struct_llama_context
}

// New loads a GGUF model file and creates an inference context.
// ctxSize is the context window in tokens (0 = model default, 512 is fine for routing).
// threads is the number of CPU threads (0 = use NumCPU).
// gpuLayers is the number of transformer layers to offload to GPU (0 = CPU-only).
func New(modelPath string, ctxSize, threads, gpuLayers int) (Engine, error) {
	C.llama_backend_init()

	mparams := C.llama_model_default_params()
	mparams.n_gpu_layers = C.int32_t(gpuLayers)

	cpath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cpath))

	model := C.llama_load_model_from_file(cpath, mparams)
	if model == nil {
		return nil, fmt.Errorf("localllm: cannot load model from %q", modelPath)
	}

	cparams := C.llama_context_default_params()
	if ctxSize > 0 {
		cparams.n_ctx = C.uint32_t(ctxSize)
	}
	if threads > 0 {
		cparams.n_threads = C.int32_t(threads)
	}

	ctx := C.llama_new_context_with_model(model, cparams)
	if ctx == nil {
		C.llama_free_model(model)
		return nil, fmt.Errorf("localllm: cannot create inference context")
	}

	return &llamaEngine{model: model, ctx: ctx}, nil
}

// Route tokenizes systemPrompt+userPrompt using a Llama-3 instruct template,
// runs greedy autoregressive generation (max 256 tokens), and returns the raw
// generated text. The caller expects a JSON string matching LLMRouterDecision.
func (e *llamaEngine) Route(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// Llama-3 instruct chat template — broadly supported by instruction-tuned GGUF models.
	combined := "<|begin_of_text|><|start_header_id|>system<|end_header_id|>\n" +
		systemPrompt +
		"<|eot_id|><|start_header_id|>user<|end_header_id|>\n" +
		userPrompt +
		"<|eot_id|><|start_header_id|>assistant<|end_header_id|>\n"

	ccombined := C.CString(combined)
	defer C.free(unsafe.Pointer(ccombined))

	const maxTokens = 4096
	tokens := make([]C.llama_token, maxTokens)

	nTokens := C.llama_tokenize(
		e.model,
		ccombined, C.int32_t(len(combined)),
		&tokens[0], C.int32_t(maxTokens),
		C.bool(true),  // add_special (BOS)
		C.bool(true),  // parse_special tokens
	)
	if nTokens < 0 {
		return "", fmt.Errorf("localllm: tokenize failed (input too long?)")
	}

	// Prefill: process all prompt tokens in one batch.
	prefillBatch := C.llama_batch_get_one(&tokens[0], nTokens)
	if C.llama_decode(e.ctx, prefillBatch) != 0 {
		return "", fmt.Errorf("localllm: prefill decode failed")
	}

	// Autoregressive generation — greedy sampler for deterministic JSON output.
	sampler := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(sampler, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(sampler)

	var out []byte
	for range 256 {
		select {
		case <-ctx.Done():
			return string(out), ctx.Err()
		default:
		}

		tok := C.llama_sampler_sample(sampler, e.ctx, -1)
		if C.llama_token_is_eog(e.model, tok) != 0 {
			break
		}

		var pieceBuf [32]C.char
		n := C.llama_token_to_piece(e.model, tok, &pieceBuf[0], C.int32_t(len(pieceBuf)), 0, C.bool(true))
		if n > 0 {
			out = append(out, C.GoStringN(&pieceBuf[0], C.int(n))...)
		}

		nextTok := tok
		nextBatch := C.llama_batch_get_one(&nextTok, 1)
		if C.llama_decode(e.ctx, nextBatch) != 0 {
			break
		}
	}

	return string(out), nil
}

func (e *llamaEngine) Close() {
	C.llama_free(e.ctx)
	C.llama_free_model(e.model)
	C.llama_backend_free()
}
