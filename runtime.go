package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// FunctionConfig defines mapping and limits from config.json
type FunctionConfig struct {
	Name             string   `json:"name"`
	WasmPath         string   `json:"wasm_path"`
	Routes           []string `json:"routes"`
	MemoryLimitPages uint32   `json:"memory_limit_pages"`
}

// Config maps the structure of config.json
type Config struct {
	Functions []FunctionConfig `json:"functions"`
}

// WasmFunction represents a pre-compiled serverless function with its own runtime sandboxing constraints
type WasmFunction struct {
	Name             string
	WasmPath         string
	Runtime          wazero.Runtime
	CompiledModule   wazero.CompiledModule
	MemoryLimitPages uint32
	LastModTime      time.Time
}

// RuntimeManager handles the lifecycles and hot-reloading of WASM sandboxes
type RuntimeManager struct {
	Ctx          context.Context
	ConfigPath   string
	FunctionsDir string

	Mu                sync.RWMutex
	Cache             map[string]*WasmFunction // Route path -> Pre-compiled Wasm Function
	ConfigLastModTime time.Time

	compiledFuncs map[string]*WasmFunction // Function Name -> Pre-compiled Wasm Function
	watcherQuit   chan struct{}
}

// NewRuntimeManager creates the manager, loads functions and starts the background file watcher.
func NewRuntimeManager(ctx context.Context, configPath string, functionsDir string) (*RuntimeManager, error) {
	rm := &RuntimeManager{
		Ctx:           ctx,
		ConfigPath:    configPath,
		FunctionsDir:  functionsDir,
		Cache:         make(map[string]*WasmFunction),
		compiledFuncs: make(map[string]*WasmFunction),
		watcherQuit:   make(chan struct{}),
	}

	// 1. Initial configuration load
	if err := rm.LoadConfigAndFunctions(); err != nil {
		return nil, fmt.Errorf("initial config load failed: %w", err)
	}

	// 2. Start background file watcher for hot-reloading
	go rm.StartWatcher()

	return rm, nil
}

// LoadConfigAndFunctions reads config.json, compiles/updates changed WASM modules, and updates routing.
func (rm *RuntimeManager) LoadConfigAndFunctions() error {
	// A. Check config file info
	cfgInfo, err := os.Stat(rm.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to check config file: %w", err)
	}
	configModTime := cfgInfo.ModTime()

	// B. Parse config file
	cfgBytes, err := os.ReadFile(rm.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return fmt.Errorf("failed to parse config JSON: %w", err)
	}

	newCompiledFuncs := make(map[string]*WasmFunction)
	newCache := make(map[string]*WasmFunction)

	// C. Compile or update functions specified in the config
	for _, fnCfg := range cfg.Functions {
		wasmPath := fnCfg.WasmPath
		if !filepath.IsAbs(wasmPath) {
			wasmPath = filepath.Clean(wasmPath)
		}

		wasmInfo, err := os.Stat(wasmPath)
		if err != nil {
			log.Printf("[Warning] Failed to find WASM file for function %s at %s: %v", fnCfg.Name, wasmPath, err)
			continue
		}
		wasmModTime := wasmInfo.ModTime()

		// Check if we already have this function compiled and unchanged
		rm.Mu.RLock()
		existing, ok := rm.compiledFuncs[fnCfg.Name]
		rm.Mu.RUnlock()

		var wasmFunc *WasmFunction
		if ok && existing.WasmPath == wasmPath && existing.MemoryLimitPages == fnCfg.MemoryLimitPages && !existing.LastModTime.Before(wasmModTime) {
			// No changes detected for this function, reuse it
			wasmFunc = existing
		} else {
			// Change detected or new function: Compile module
			log.Printf("[Info] Compiling WASM module '%s' (RAM limit: %d pages = %d KB)...", fnCfg.Name, fnCfg.MemoryLimitPages, fnCfg.MemoryLimitPages*64)

			// Create wazero runtime for this specific function with its memory limits
			rConfig := wazero.NewRuntimeConfig().WithMemoryLimitPages(fnCfg.MemoryLimitPages)
			r := wazero.NewRuntimeWithConfig(rm.Ctx, rConfig)

			// Standard WASI snapshot preview1 instantiation
			_, err = wasi_snapshot_preview1.Instantiate(rm.Ctx, r)
			if err != nil {
				r.Close(rm.Ctx)
				log.Printf("[Error] Failed to instantiate WASI for '%s': %v", fnCfg.Name, err)
				// Reuse old version if compiling new one failed
				if ok {
					wasmFunc = existing
				} else {
					continue
				}
			} else {
				// Read new bytecode
				bytecode, err := os.ReadFile(wasmPath)
				if err != nil {
					r.Close(rm.Ctx)
					log.Printf("[Error] Failed to read WASM bytecode for '%s': %v", fnCfg.Name, err)
					if ok {
						wasmFunc = existing
					} else {
						continue
					}
				} else {
					// Compile
					compiled, err := r.CompileModule(rm.Ctx, bytecode)
					if err != nil {
						r.Close(rm.Ctx)
						log.Printf("[Error] Failed to compile WASM module '%s': %v", fnCfg.Name, err)
						if ok {
							wasmFunc = existing
						} else {
							continue
						}
					} else {
						// Success compiling new version!
						wasmFunc = &WasmFunction{
							Name:             fnCfg.Name,
							WasmPath:         wasmPath,
							Runtime:          r,
							CompiledModule:   compiled,
							MemoryLimitPages: fnCfg.MemoryLimitPages,
							LastModTime:      wasmModTime,
						}

						// Close old runtime if it exists
						if ok && existing.Runtime != nil {
							log.Printf("[Info] Closing old runtime instance for function '%s'", fnCfg.Name)
							_ = existing.Runtime.Close(rm.Ctx)
						}
					}
				}
			}
		}

		if wasmFunc != nil {
			newCompiledFuncs[fnCfg.Name] = wasmFunc
			for _, r := range fnCfg.Routes {
				newCache[r] = wasmFunc
			}
		}
	}

	// D. Atomically swap cache and update mod times
	rm.Mu.Lock()
	oldCompiledFuncs := rm.compiledFuncs
	rm.compiledFuncs = newCompiledFuncs
	rm.Cache = newCache
	rm.ConfigLastModTime = configModTime
	rm.Mu.Unlock()

	// E. Close runtimes of compiled functions that have been completely removed from config
	for name, oldFn := range oldCompiledFuncs {
		if _, stillExists := newCompiledFuncs[name]; !stillExists {
			log.Printf("[Info] Function '%s' removed from config. Closing its runtime.", name)
			_ = oldFn.Runtime.Close(rm.Ctx)
		}
	}

	return nil
}

// GetFunction retrieves a cached WebAssembly function for a route (Thread-Safe)
func (rm *RuntimeManager) GetFunction(route string) (*WasmFunction, bool) {
	rm.Mu.RLock()
	defer rm.Mu.RUnlock()
	fn, ok := rm.Cache[route]
	return fn, ok
}

// StartWatcher polls config.json and .wasm files for changes every second
func (rm *RuntimeManager) StartWatcher() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	log.Println("[Info] Background watcher started for hot-reloading.")

	for {
		select {
		case <-rm.watcherQuit:
			return
		case <-ticker.C:
			rm.checkFiles()
		}
	}
}

// checkFiles inspects config.json and Wasm files for changes
func (rm *RuntimeManager) checkFiles() {
	// Check if config.json was modified
	cfgInfo, err := os.Stat(rm.ConfigPath)
	if err != nil {
		return // Ignore transient read errors
	}

	rm.Mu.RLock()
	lastCfgModTime := rm.ConfigLastModTime
	compiledMap := make(map[string]*WasmFunction)
	for k, v := range rm.compiledFuncs {
		compiledMap[k] = v
	}
	rm.Mu.RUnlock()

	configChanged := cfgInfo.ModTime().After(lastCfgModTime)
	wasmChanged := false

	// Check if any of our active wasm files changed
	if !configChanged {
		for _, fn := range compiledMap {
			wasmInfo, err := os.Stat(fn.WasmPath)
			if err != nil {
				continue
			}
			if wasmInfo.ModTime().After(fn.LastModTime) {
				wasmChanged = true
				log.Printf("[Info] Hot-reloaded detection: WASM file for '%s' was updated.", fn.Name)
				break
			}
		}
	}

	if configChanged || wasmChanged {
		if configChanged {
			log.Println("[Info] Hot-reloaded detection: config.json was updated.")
		}
		if err := rm.LoadConfigAndFunctions(); err != nil {
			log.Printf("[Error] Hot-reloading failed: %v", err)
		} else {
			log.Println("[Info] Hot-reloading completed successfully.")
		}
	}
}

// Close releases all runtimes and stops the file watcher
func (rm *RuntimeManager) Close() error {
	close(rm.watcherQuit)

	rm.Mu.Lock()
	defer rm.Mu.Unlock()

	for name, fn := range rm.compiledFuncs {
		log.Printf("[Info] Closing runtime for function '%s'", name)
		_ = fn.Runtime.Close(rm.Ctx)
	}

	return nil
}
