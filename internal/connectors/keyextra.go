package connectors

import "strings"

// DeriveKeyExtra derives per-connection extra values from a pasted API key, per the provider's
// key_extra rules. Only the "suffix" rule is supported: the substring after the last '-' in the
// key (Mailchimp keys are "<secret>-<dc>"). Unknown rules and dashless keys yield "".
func DeriveKeyExtra(prov Provider, key string) map[string]string {
	if len(prov.KeyExtra) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, rule := range prov.KeyExtra {
		switch rule {
		case "suffix":
			if i := strings.LastIndex(key, "-"); i >= 0 && i < len(key)-1 {
				out[k] = key[i+1:]
			} else {
				out[k] = ""
			}
		default:
			out[k] = ""
		}
	}
	return out
}
