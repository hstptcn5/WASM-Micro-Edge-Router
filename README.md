# WASM Micro-Edge Router 🌀

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Cross--Platform-lightgrey.svg)](https://github.com/tetratelabs/wazero)

> **Docker is great, but do you really need a 100MB container just to run a 50-line webhook?**

**WASM Micro-Edge Router** is a local-first, zero-dependency serverless runtime engineered in Go. It enables you to build, deploy, and scale untrusted user-submitted microservices inside secure, isolated WebAssembly (WASM) sandboxes in **under 10 milliseconds** with close to 0MB idle RAM overhead.

---

## 💡 Why This Project?

While the serverless WebAssembly ecosystem is heavily dominated by Rust & Wasmtime (e.g., Fermyon Spin, Fastly Compute), this project demonstrates that **Go can do it too—and do it cleanly with zero CGO dependencies**.

By utilizing `wazero` (a 100% pure Go WebAssembly runtime) and a custom memory-bridge protocol, we successfully eliminated massive container cold starts, achieving:
- **Instant cold starts (~7ms)** including sandbox runtime instantiation.
- **Zero idle RAM overhead** as sandboxes are destroyed immediately after execution.
- **Pure Go execution** with no CGO dependencies, compiling down to a single, static binary.

---

## 🌟 Key Features

* **100% Pure Go Sandbox**: Built on `wazero` for CGO-free, secure execution of WebAssembly bytecode.
* **Custom Memory Bridge Protocol**: Implements high-throughput pointer-passing over WebAssembly linear memory, allowing rich HTTP payloads (headers, method, url, body) to be processed by guests.
* **Dynamic Configuration (`config.json`)**: Map routes, configure files, and apply custom memory page limits dynamically per function.
* **Resilient Hot-Reloading Watcher**: Background directory watcher automatically compiles newly added/modified `.wasm` guest modules and hot-swaps routing maps atomically under load without server downtime.
* **Safe Trap & Panic Recovery**: Catches guest sandbox traps, runtime panics, or memory violations gracefully, returning an HTTP 500 error and continuing router execution uninterrupted.

---

## 🏗️ Technical Architecture

```mermaid
graph TD
    Client[HTTP Client] -->|HTTP Request| Router[HTTP Server: main.go]
    Router -->|1. Lookup Route| RuntimeMgr[Runtime Manager: runtime.go]
    RuntimeMgr -->|2. Check Cache / config.json| Cache[WasmFunction Cache]
    Router -->|3. Instantiate Sandbox| Guest[WASM Sandbox Instance: hello.wasm]
    Router -->|4. Allocate Memory & Write Payload| Bridge[Memory Bridge: bridge.go]
    Bridge -->|5. Invoke handler()| Guest
    Guest -->|6. Return Packed Pointer & Length| Bridge
    Bridge -->|7. Read Memory & Decode Response| Router
    Router -->|8. HTTP Response| Client
    Router -.->|9. Close() Sandbox| Guest
```

---

## 🚀 Quick Start

### 1. Prerequisites
Ensure you have Go installed (v1.22+).

### 2. Clone the Repository
```bash
git clone https://github.com/YOUR_USERNAME/wasm-edge-router.git
cd wasm-edge-router
```

### 3. Compile Guest WASM Functions
Guest functions are compiled targeting `wasip1` as **WASI Reactors** using `-buildmode=c-shared` to prevent immediate exit and run indefinitely:

```bash
# Compile Hello Guest
$env:GOOS="wasip1"; $env:GOARCH="wasm"; go build -buildmode=c-shared -o functions/hello.wasm functions/hello/main.go

# Compile Markdown Parser Guest
$env:GOOS="wasip1"; $env:GOARCH="wasm"; go build -buildmode=c-shared -o functions/markdown.wasm functions/markdown/main.go
```

### 4. Run the Edge Router Server
```bash
go run .
```
The server will boot, compile your functions from `functions/`, and listen on port `8080`.

---

## 🧪 Verification & Test Examples

### A. GET Hello Greeting Request
```bash
curl.exe -i http://localhost:8080/api/greet
```
**Response**:
```http
HTTP/1.1 200 OK
Access-Control-Allow-Origin: *
Content-Type: application/json
X-Powered-By: wasm-edge-router

{
    "message": "Hello from hot-reloaded WASM edge!",
    "route_accessed": "/api/greet",
    "request_method": "GET"
}
```

### B. POST Markdown-to-HTML Request
```bash
curl.exe -i -X POST -H "Content-Type: text/plain" -d "Double asterisk is **bold** and single is *italic*." http://localhost:8080/api/markdown
```
**Response**:
```http
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
X-Service: wasm-markdown-parser

<p>Double asterisk is <strong>bold</strong> and single is <em>italic</em>.</p>
```

---

## ⚙️ Configuration (`config.json`)

Configure memory constraints (in WASM 64KB pages) and routing endpoints in the root config file:

```json
{
  "functions": [
    {
      "name": "hello",
      "wasm_path": "functions/hello.wasm",
      "routes": ["/hello", "/api/greet"],
      "memory_limit_pages": 256
    },
    {
      "name": "markdown",
      "wasm_path": "functions/markdown.wasm",
      "routes": ["/api/markdown"],
      "memory_limit_pages": 256
    }
  ]
}
```
Any modifications to `config.json` or updates to the `.wasm` files are automatically picked up by the background file watcher and reloaded instantly.
