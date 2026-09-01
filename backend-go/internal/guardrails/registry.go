package guardrails

import (
	"log"
	"sort"
	"strings"
	"sync"
)

const disabledGuardrailsHeader = "X-Ccx-Disabled-Guardrails"

// Registry 是按优先级排序的 guardrail 注册表。
// 并发安全，可在运行时注册/替换。
type Registry struct {
	mu         sync.RWMutex
	guardrails []Guardrail
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{}
}

// Register 注册一个 guardrail；同名旧实例被替换。
// 注册后按 priority 升序重排。
func (r *Registry) Register(g Guardrail) {
	if g == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	name := normalizeName(g.Name())
	// 替换同名
	filtered := r.guardrails[:0]
	for _, existing := range r.guardrails {
		if normalizeName(existing.Name()) != name {
			filtered = append(filtered, existing)
		}
	}
	filtered = append(filtered, g)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Priority() < filtered[j].Priority()
	})
	r.guardrails = filtered
}

// List 返回当前所有 guardrail 的副本（按优先级排序）。
func (r *Registry) List() []Guardrail {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Guardrail, len(r.guardrails))
	copy(out, r.guardrails)
	return out
}

// RunPreCall 按优先级执行所有启用且未豁免的 guardrail 的 PreCall。
// 任一 guardrail 报错或 panic 均 fail-open（记日志放行）。
// 返回最终 payload 与所有执行结果。
func (r *Registry) RunPreCall(payload []byte, ctx *Context) ([]byte, []*ExecutionResult) {
	guardrails := r.snapshot()
	disabled := parseDisabled(ctx)

	results := make([]*ExecutionResult, 0, len(guardrails))
	current := payload

	for _, g := range guardrails {
		name := g.Name()
		if !g.Enabled() {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   true,
				Stage:     "pre",
				Reason:    "disabled",
			})
			continue
		}
		if disabled[normalizeName(name)] {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   true,
				Stage:     "pre",
				Reason:    "opt-out",
			})
			continue
		}

		result, err := safePreCall(g, current, ctx)
		if err != nil {
			log.Printf("[Guardrail-PreCall] %s 执行异常，fail-open: %v", name, err)
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   false,
				Stage:     "pre",
				Error:     err.Error(),
				FailOpen:  true,
			})
			continue
		}
		if result == nil {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   false,
				Stage:     "pre",
			})
			continue
		}
		if result.Blocked {
			// credential-masker 永远不会 block，但未来的 guardrail 可能会
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Blocked:   true,
				Message:   result.Message,
				Stage:     "pre",
				Meta:      result.Meta,
			})
			// 被拦截时立即返回，不再继续后续 guardrail
			return current, results
		}
		if result.Modified && result.Payload != nil {
			current = result.Payload
		}
		results = append(results, &ExecutionResult{
			Guardrail: name,
			Modified:  result.Modified,
			Message:   result.Message,
			Stage:     "pre",
			Meta:      result.Meta,
		})
	}

	return current, results
}

// RunPostCall 按优先级执行所有启用且未豁免的 guardrail 的 PostCall。
// 语义与 RunPreCall 对称。
func (r *Registry) RunPostCall(response []byte, ctx *Context) ([]byte, []*ExecutionResult) {
	guardrails := r.snapshot()
	disabled := parseDisabled(ctx)

	results := make([]*ExecutionResult, 0, len(guardrails))
	current := response

	for _, g := range guardrails {
		name := g.Name()
		if !g.Enabled() {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   true,
				Stage:     "post",
				Reason:    "disabled",
			})
			continue
		}
		if disabled[normalizeName(name)] {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   true,
				Stage:     "post",
				Reason:    "opt-out",
			})
			continue
		}

		result, err := safePostCall(g, current, ctx)
		if err != nil {
			log.Printf("[Guardrail-PostCall] %s 执行异常，fail-open: %v", name, err)
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   false,
				Stage:     "post",
				Error:     err.Error(),
				FailOpen:  true,
			})
			continue
		}
		if result == nil {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Skipped:   false,
				Stage:     "post",
			})
			continue
		}
		if result.Blocked {
			results = append(results, &ExecutionResult{
				Guardrail: name,
				Blocked:   true,
				Message:   result.Message,
				Stage:     "post",
				Meta:      result.Meta,
			})
			return current, results
		}
		if result.Modified && result.Response != nil {
			current = result.Response
		}
		results = append(results, &ExecutionResult{
			Guardrail: name,
			Modified:  result.Modified,
			Message:   result.Message,
			Stage:     "post",
			Meta:      result.Meta,
		})
	}

	return current, results
}

func (r *Registry) snapshot() []Guardrail {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Guardrail, len(r.guardrails))
	copy(out, r.guardrails)
	return out
}

// ExecutionResult 描述单个 guardrail 的执行结果（用于审计与日志）。
type ExecutionResult struct {
	Guardrail string
	Blocked   bool
	Modified  bool
	Skipped   bool
	FailOpen  bool
	Message   string
	Error     string
	Reason    string // disabled / opt-out
	Stage     string // pre / post
	Meta      map[string]any
}

// safePreCall 带 panic recover 的 PreCall 包装。
func safePreCall(g Guardrail, payload []byte, ctx *Context) (result *Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &guardrailPanicError{name: g.Name(), value: r}
		}
	}()
	return g.PreCall(payload, ctx)
}

// safePostCall 带 panic recover 的 PostCall 包装。
func safePostCall(g Guardrail, response []byte, ctx *Context) (result *Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &guardrailPanicError{name: g.Name(), value: r}
		}
	}()
	return g.PostCall(response, ctx)
}

type guardrailPanicError struct {
	name  string
	value any
}

func (e *guardrailPanicError) Error() string {
	return "panic in " + e.name
}

// normalizeName 将 guardrail 名称规范化为小写 kebab-case。
func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

// parseDisabled 从请求头解析豁免的 guardrail 名称列表。
func parseDisabled(ctx *Context) map[string]bool {
	if ctx == nil || ctx.Headers == nil {
		return nil
	}
	val := ctx.Headers.Get(disabledGuardrailsHeader)
	if val == "" {
		return nil
	}
	disabled := make(map[string]bool)
	for _, part := range strings.Split(val, ",") {
		name := normalizeName(part)
		if name != "" {
			disabled[name] = true
		}
	}
	return disabled
}
