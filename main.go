package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tetratelabs/wazero"
)

func main() {
	log.Println("[Info] Starting WASM Micro-Edge Router...")

	ctx := context.Background()
	functionsDir := "./functions"

	// Initialize the runtime manager and compile WASM functions
	rm, err := NewRuntimeManager(ctx, "config.json", functionsDir)
	if err != nil {
		log.Fatalf("[Critical] Failed to initialize runtime manager: %v", err)
	}
	defer rm.Close()

	// Define HTTP Router handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := r.URL.Path

		// Match route
		wasmFunc, ok := rm.GetFunction(path)
		if !ok {
			http.Error(w, fmt.Sprintf("Route not found: %s", path), http.StatusNotFound)
			log.Printf("[Info] 404 Route Not Found: %s %s (Duration: %v)", r.Method, path, time.Since(start))
			return
		}

		// Handle request inside WASM sandbox with recovery
		err := executeWasmHandler(r.Context(), wasmFunc, w, r)
		if err != nil {
			log.Printf("[Error] Failed executing WASM handler for %s: %v", path, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		log.Printf("[Info] 200 Handled: %s %s (Duration: %v)", r.Method, path, time.Since(start))
	})

	log.Println("[Info] HTTP server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("[Critical] HTTP server failed: %v", err)
	}
}

// executeWasmHandler instantiates a sandbox module, injects payload, calls guest handler, and writes response.
func executeWasmHandler(ctx context.Context, fn *WasmFunction, w http.ResponseWriter, r *http.Request) (retErr error) {
	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read HTTP request body: %w", err)
	}

	// Build request payload struct
	wasmReq := &WasmHTTPRequest{
		Method:  r.Method,
		URL:     r.URL.String(),
		Headers: r.Header,
		Body:    string(bodyBytes),
	}

	// Configure and instantiate the transient module
	// Ensure isolation: No host directories or env vars mapped.
	config := wazero.NewModuleConfig().
		WithStdout(os.Stdout).
		WithStderr(os.Stderr)

	mod, err := fn.Runtime.InstantiateModule(ctx, fn.CompiledModule, config)
	if err != nil {
		return fmt.Errorf("failed to instantiate WASM module: %w", err)
	}
	// Ensure we release the module sandbox RAM immediately upon exit
	defer func() {
		if closeErr := mod.Close(ctx); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("failed to close WASM module: %w", closeErr)
		}
	}()

	// Handle traps or runtime panics inside the WASM module
	defer func() {
		if rec := recover(); rec != nil {
			retErr = fmt.Errorf("WASM sandbox panicked/trapped: %v", rec)
		}
	}()

	// 1. Call _initialize if exported (WASI Reactor buildmode)
	initFunc := mod.ExportedFunction("_initialize")
	if initFunc != nil {
		if _, err := initFunc.Call(ctx); err != nil {
			return fmt.Errorf("failed to initialize Go runtime in WASM guest: %w", err)
		}
	}

	// 2. Get the exported allocate and handler functions
	allocFunc := mod.ExportedFunction("allocate")
	if allocFunc == nil {
		return fmt.Errorf("WASM module does not export 'allocate' function")
	}

	handlerFunc := mod.ExportedFunction("handler")
	if handlerFunc == nil {
		return fmt.Errorf("WASM module does not export 'handler' function")
	}

	// 3. Write request payload to WASM memory
	offset, length, err := WriteRequestPayload(ctx, mod, allocFunc, wasmReq)
	if err != nil {
		return fmt.Errorf("failed to write request payload: %w", err)
	}

	// 4. Call handler
	results, err := handlerFunc.Call(ctx, uint64(offset), uint64(length))
	if err != nil {
		return fmt.Errorf("failed to call WASM handler: %w", err)
	}
	if len(results) != 1 {
		return fmt.Errorf("expected 1 result from handler, got %v", results)
	}

	// 5. Read response payload from WASM memory
	wasmResp, err := ReadResponsePayload(mod, results[0])
	if err != nil {
		return fmt.Errorf("failed to read response payload: %w", err)
	}

	// 6. Write response headers, status code, and body back to HTTP client
	for k, values := range wasmResp.Headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(wasmResp.Status)
	_, _ = w.Write([]byte(wasmResp.Body))

	return nil
}
