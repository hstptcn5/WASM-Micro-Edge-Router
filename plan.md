Dưới đây là bản Đặc tả Kỹ thuật (Technical Specification) toàn diện và chi tiết nhất cho dự án WASM Micro-Edge Router. Bạn chỉ cần sao chép toàn bộ nội dung trong khung văn bản bên dưới và gửi cho AI Agent (Cursor, Claude, hoặc v0), nó sẽ hiểu chính xác kiến trúc, cấu trúc bộ nhớ và các bước cần triển khai.

Plaintext
Act as an Expert Systems Engineer and Cloud Infrastructure Architect in Golang. Your task is to build a high-performance, ultra-lightweight, local-first Serverless Edge Router in Go named `wasm-edge-router`. This system replaces heavy Docker containers by running untrusted user functions inside WebAssembly (WASM) sandboxes using the pure Go runtime `wazero`. 

Follow the strict technical specifications, shared-memory protocols, and implementation steps detailed below.

---

### 1. Project Topology & Dependencies
- Language: Go (Golang) 1.22+
- WASM Runtime: Use the official 100% pure Go runtime `github.com/tetratelabs/wazero`. Do NOT use CGO-based runtimes like Wasmtime or Wasmer.
- HTTP Server: Use the standard `net/http` package or `github.com/valyala/fasthttp` for high-throughput ingress routing.
- Modular File Tree Structure:
  - `main.go`: Application entry point, HTTP server initialization, configuration loading, and router orchestration.
  - `runtime.go`: Module compilation, instantiation pool, worker scaling, and runtime configuration using wazero.
  - `bridge.go`: Low-level Linear Memory Bridge management (reading/writing bytes between Go Host and WASM Guest).

---

### 2. Core Structs & Shared Memory Protocol
WASM executes within an isolated linear memory space. Because WASM natively only understands primitive numbers (i32, i64), you must implement a Custom Memory Bridge Protocol to pass rich HTTP payloads (JSON strings) into and out of the WASM Guest.

Implement the following structures in `main.go` and `runtime.go`:

```go
package main

import (
	"context"
	"[github.com/tetratelabs/wazero](https://github.com/tetratelabs/wazero)"
)

// WasmFunction represents a pre-compiled serverless function stored in memory
type WasmFunction struct {
	Name           string
	WasmBytecode   []byte
	CompiledModule wazero.CompiledModule
}

// RuntimeManager handles the lifecycles of wazero sandboxes
type RuntimeManager struct {
	Runtime wazero.Runtime
	Ctx     context.Context
	Cache   map[string]*WasmFunction // URL Path -> Pre-compiled Wasm Function
}
The Memory Bridge Protocol Rules:
Allocation: The Go Host must call an exported WASM function allocate(size uint32) uint32 to request a safe memory region inside the WASM linear memory. The WASM Guest returns the offset (pointer) of the allocated space.

Writing Input: The Go Host uses mod.Memory().Write(offset, requestPayloadBytes) to write the incoming HTTP Request (formatted as JSON string) directly into the WASM instance's RAM.

Execution: The Go Host calls the entry point function exported by the WASM Guest: handler(offset uint32, length uint32) uint64.

Reading Output: The WASM Guest processes the request, serializes the HTTP Response (Status, Headers, Body) into a JSON string, and returns a single packed uint64 value. The upper 32 bits represent the response_offset, and the lower 32 bits represent the response_length. The Go Host reads this memory region and writes it back to the client.

3. Step-by-Step Implementation Flow
Step 1: Pre-compilation & Route Mapping (runtime.go)
Upon startup, read all .wasm files from a dedicated functions/ directory.

For each file, initialize wazero.NewRuntime(ctx) and compile the raw bytecode into a wazero.CompiledModule via r.CompileModule(ctx, bytecode).

Cache these compiled modules in a global memory map using their filenames/routes as keys. Compiling once at startup eliminates cold-start overhead down to microseconds.

Step 2: HTTP Ingress Hooking (main.go)
Run an HTTP server listening on port 8080.

Extract the request URL path (e.g., /api/hello). Match it against the cached WasmFunction routing map.

If a match is found, dynamically create a transient sandbox instance using r.InstantiateModule(ctx, compiledModule, config).

Step 3: Payload Injection & Handler Execution (bridge.go)
Read the incoming HTTP Request body, headers, and method. Convert this data into a standardized JSON string.

Execute the Memory Bridge Protocol (Allocate space -> Write request bytes -> Invoke the exported handler function).

Unpack the returned uint64 to find the location and size of the response inside the sandbox.

Step 4: Graceful Demolition & Zero-Idle RAM Caching
Read the response bytes out of the WASM linear memory and parse them to construct the final HTTP response sent to the client.

Close the transient WASM instance immediately using mod.Close(ctx). This completely purges the allocation table and frees all linear memory sandbox spaces back to the OS, maintaining close to 0MB idle RAM overhead.

4. Sandbox Hardening & Safety Requirements
Host Functions Constraint: By default, do NOT grant the WASM guest functions access to the Host's disk or network via WASI (WebAssembly System Interface) unless explicitly configured. The guest function must remain completely sandboxed.

Memory Boundaries: Configure a strict memory limit for each guest function using wazero.NewModuleConfig().WithMaxMemoryPages(1). One page equals 64KB, allowing tiny micro-services to run inside extremely tight boundaries.

Error Propagation: If a guest function crashes, panics, or causes a memory out-of-bounds error, catch the trap gracefully, return an HTTP 500 Internal Server Error, log the issue, and ensure the router engine continues running uninterrupted.

Begin generating the complete Go codebase now. Implement the architecture in a clean, highly documented, modular layout.