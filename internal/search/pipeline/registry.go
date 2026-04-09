package pipeline

import "sync"

// Registry 管线注册表 / Pipeline registry
type Registry struct {
	mu        sync.RWMutex
	pipelines map[string]*Pipeline
}

// NewRegistry 创建管线注册表 / Create a new pipeline registry
func NewRegistry() *Registry {
	return &Registry{
		pipelines: make(map[string]*Pipeline),
	}
}

// Register 注册管线 / Register a pipeline
func (r *Registry) Register(p *Pipeline) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pipelines[p.Name] = p
}

// Get 获取管线（未找到返回 nil）/ Get pipeline by name (nil if not found)
func (r *Registry) Get(name string) *Pipeline {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pipelines[name]
}

// Names 返回已注册管线名称列表 / Return list of registered pipeline names
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.pipelines))
	for name := range r.pipelines {
		names = append(names, name)
	}
	return names
}
