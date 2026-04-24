package pool

import "strings"

// SanitizePools validates and normalizes cross-provider pool configuration,
// dropping entries with empty aliases or no valid models and deduplicating
// aliases.
func SanitizePools(pools []CrossProviderPool) []CrossProviderPool {
	if len(pools) == 0 {
		return pools
	}
	out := make([]CrossProviderPool, 0, len(pools))
	seenAliases := make(map[string]struct{}, len(pools))

	for i := range pools {
		p := pools[i]
		p.Alias = strings.TrimSpace(p.Alias)
		if p.Alias == "" {
			continue
		}
		if _, exists := seenAliases[p.Alias]; exists {
			continue
		}
		seenAliases[p.Alias] = struct{}{}

		validModels := make([]CrossProviderPoolModel, 0, len(p.Models))
		for j := range p.Models {
			m := p.Models[j]
			m.Provider = strings.TrimSpace(m.Provider)
			m.Model = strings.TrimSpace(m.Model)
			if m.Provider != "" && m.Model != "" {
				validModels = append(validModels, m)
			}
		}
		if len(validModels) == 0 {
			continue
		}
		p.Models = validModels
		out = append(out, p)
	}
	return out
}
