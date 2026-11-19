// Package i18n loads UI string catalogues keyed by message ID (plan §7.7).
// The same catalogues serve Go templates and the Svelte app (via /api/v1/i18n/{locale}),
// so there is one source of truth for every user-facing string.
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// DefaultLocale is the launch language; other locales fall back to it per key.
const DefaultLocale = "en"

// Catalogue holds flattened messages per locale.
type Catalogue struct {
	messages map[string]map[string]string
}

// Load reads every <locale>.json file in dir of fsys. Nested objects are flattened with dots.
func Load(fsys fs.FS, dir string) (*Catalogue, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	c := &Catalogue{messages: map[string]map[string]string{}}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("i18n %s: %w", f, err)
		}
		flat := map[string]string{}
		if err := flatten("", raw, flat); err != nil {
			return nil, fmt.Errorf("i18n %s: %w", f, err)
		}
		c.messages[strings.TrimSuffix(path.Base(f), ".json")] = flat
	}
	if _, ok := c.messages[DefaultLocale]; !ok {
		return nil, fmt.Errorf("i18n: default locale %q catalogue missing", DefaultLocale)
	}
	return c, nil
}

func flatten(prefix string, in map[string]any, out map[string]string) error {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch t := v.(type) {
		case string:
			out[key] = t
		case map[string]any:
			if err := flatten(key, t, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("key %q: values must be strings or objects", key)
		}
	}
	return nil
}

// Locales returns the available locales, sorted.
func (c *Catalogue) Locales() []string {
	out := make([]string, 0, len(c.messages))
	for l := range c.messages {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a locale catalogue exists.
func (c *Catalogue) Has(locale string) bool { _, ok := c.messages[locale]; return ok }

// Messages returns the merged messages for a locale (default locale as fallback).
func (c *Catalogue) Messages(locale string) map[string]string {
	out := make(map[string]string, len(c.messages[DefaultLocale]))
	for k, v := range c.messages[DefaultLocale] {
		out[k] = v
	}
	for k, v := range c.messages[locale] {
		out[k] = v
	}
	return out
}

// Keys returns the message IDs defined for a locale (no fallback).
func (c *Catalogue) Keys(locale string) []string {
	out := make([]string, 0, len(c.messages[locale]))
	for k := range c.messages[locale] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// T translates key for locale, substituting {name} placeholders from args (name, value pairs).
// Missing keys render as the key itself so gaps are visible rather than silent.
func (c *Catalogue) T(locale, key string, args ...any) string {
	msg, ok := c.messages[locale][key]
	if !ok {
		msg, ok = c.messages[DefaultLocale][key]
	}
	if !ok {
		return key
	}
	for i := 0; i+1 < len(args); i += 2 {
		name, _ := args[i].(string)
		msg = strings.ReplaceAll(msg, "{"+name+"}", fmt.Sprint(args[i+1]))
	}
	return msg
}
