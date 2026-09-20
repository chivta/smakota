package ingest

import "fmt"

// Registry resolves scheduler and CLI job names to adapters.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry constructs a registry and indexes each adapter by its name.
func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{adapters: make(map[string]Adapter, len(adapters))}
	for _, adapter := range adapters {
		if adapter == nil {
			return nil, fmt.Errorf("register nil ingest adapter")
		}
		err := registry.Register(adapter.Name(), adapter)
		if err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register maps a CLI or scheduler job name to an adapter. More than one job
// name may point at the same adapter, as with OFF delta and full runs.
func (r *Registry) Register(name string, adapter Adapter) error {
	if adapter == nil {
		return fmt.Errorf("register nil ingest adapter")
	}
	if name == "" {
		return fmt.Errorf("register ingest adapter with empty name")
	}
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("duplicate ingest adapter %q", name)
	}
	r.adapters[name] = adapter
	return nil
}

// Resolve returns the adapter registered under a job name.
func (r *Registry) Resolve(name string) (Adapter, error) {
	adapter, ok := r.adapters[name]
	if !ok {
		return nil, fmt.Errorf("unknown ingest job %q", name)
	}
	return adapter, nil
}
