package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// WasmHTTPRequest represents the HTTP request payload passed into the WASM guest
type WasmHTTPRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// WasmHTTPResponse represents the HTTP response payload returned from the WASM guest
type WasmHTTPResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// WriteRequestPayload serializes the request, allocates memory in WASM, and writes the bytes.
// It returns the offset and length of the allocated memory space in WASM.
func WriteRequestPayload(ctx context.Context, mod api.Module, allocFunc api.Function, req *WasmHTTPRequest) (uint32, uint32, error) {
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	reqLen := uint32(len(reqBytes))
	results, err := allocFunc.Call(ctx, uint64(reqLen))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to call allocate function: %w", err)
	}
	if len(results) != 1 {
		return 0, 0, fmt.Errorf("expected 1 result from allocate, got %v", results)
	}

	offset := uint32(results[0])

	// Write payload into WASM linear memory
	if ok := mod.Memory().Write(offset, reqBytes); !ok {
		return 0, 0, fmt.Errorf("failed to write request bytes to memory at offset %d", offset)
	}

	return offset, reqLen, nil
}

// ReadResponsePayload reads the serialized response from WASM memory and deserializes it.
func ReadResponsePayload(mod api.Module, packedResult uint64) (*WasmHTTPResponse, error) {
	respOffset := uint32(packedResult >> 32)
	respLength := uint32(packedResult)

	if respLength == 0 {
		return nil, fmt.Errorf("empty response received from handler (length is 0)")
	}

	respBytes, ok := mod.Memory().Read(respOffset, respLength)
	if !ok {
		return nil, fmt.Errorf("failed to read response bytes at offset %d, len %d", respOffset, respLength)
	}

	var resp WasmHTTPResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response JSON: %w (raw response: %s)", err, string(respBytes))
	}

	return &resp, nil
}
