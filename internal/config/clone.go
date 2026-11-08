package config

import "routenest/internal/domain"

// Clone returns a deep copy of p. Writers use it to build a new configuration
// that is published whole, so readers holding the previous pointer never see a
// partial edit. Nil slices stay nil and empty slices stay empty.
func (p *Proxy) Clone() *Proxy {
	if p == nil {
		return nil
	}
	c := *p
	c.Headers = cloneStringMap(p.Headers)
	c.ModelChain = cloneSlice(p.ModelChain)
	c.Combos = cloneCombosDeep(p.Combos)
	c.ModelAliases = cloneSlice(p.ModelAliases)
	c.ProviderModelPrefixes = cloneStringMap(p.ProviderModelPrefixes)
	c.ExcludedModels = cloneSlice(p.ExcludedModels)
	if p.RequestErrorRules != nil {
		c.RequestErrorRules = make([]RequestErrorRule, len(p.RequestErrorRules))
		for i, r := range p.RequestErrorRules {
			r.StatusCodes = cloneSlice(r.StatusCodes)
			c.RequestErrorRules[i] = r
		}
	}
	return &c
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func cloneCombosDeep(in []domain.ModelCombo) []domain.ModelCombo {
	if in == nil {
		return nil
	}
	out := make([]domain.ModelCombo, len(in))
	for i, c := range in {
		c.Entries = cloneSlice(c.Entries)
		out[i] = c
	}
	return out
}
