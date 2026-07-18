package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// WasmHTTPRequest represents the HTTP request payload passed from the host
type WasmHTTPRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// WasmHTTPResponse represents the HTTP response payload returned to the host
type WasmHTTPResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// Package level buffers to prevent garbage collection between host calls
var requestBuffer []byte
var responseBuffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	requestBuffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&requestBuffer[0])))
}

//go:wasmexport handler
func handler(offset uint32, length uint32) uint64 {
	// 1. Read request bytes from host memory
	reqBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(offset))), length)

	// 2. Deserialize request
	var req WasmHTTPRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		return serializeResponse(&WasmHTTPResponse{
			Status: 400,
			Headers: map[string][]string{
				"Content-Type": {"text/plain"},
			},
			Body: fmt.Sprintf("WASM failed to parse request JSON: %v", err),
		})
	}

	// 3. Process request (build a customized message)
	greeting := "Hello from hot-reloaded WASM edge!"
	if req.Body != "" {
		greeting = fmt.Sprintf("Hello! You sent a body: %s", req.Body)
	}

	responseBody := fmt.Sprintf(`{
		"message": %q,
		"route_accessed": %q,
		"request_method": %q
	}`, greeting, req.URL, req.Method)

	resp := &WasmHTTPResponse{
		Status: 200,
		Headers: map[string][]string{
			"Content-Type":                 {"application/json"},
			"X-Powered-By":                 {"wasm-edge-router"},
			"Access-Control-Allow-Origin": {"*"},
		},
		Body: responseBody,
	}

	return serializeResponse(resp)
}

func serializeResponse(resp *WasmHTTPResponse) uint64 {
	respBytes, err := json.Marshal(resp)
	if err != nil {
		// Fallback simple response if marshalling itself fails
		fallback := []byte(`{"status": 500, "headers": {}, "body": "internal guest marshalling error"}`)
		responseBuffer = fallback
	} else {
		responseBuffer = respBytes
	}

	respOffset := uint32(uintptr(unsafe.Pointer(&responseBuffer[0])))
	respLength := uint32(len(responseBuffer))

	return (uint64(respOffset) << 32) | uint64(respLength)
}

func main() {}
