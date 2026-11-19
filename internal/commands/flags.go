package commands

import "sort"

// Flags holds parsed --flag values; boolean flags are present with an empty
// value.
type Flags struct {
	values map[string]string
	set    map[string]bool
}

func NewFlags() *Flags {
	return &Flags{values: map[string]string{}, set: map[string]bool{}}
}

func (f *Flags) Set(key, value string) {
	f.values[key] = value
	f.set[key] = true
}

func (f *Flags) Has(key string) bool   { return f.set[key] }
func (f *Flags) Get(key string) string { return f.values[key] }

// Bool reports whether a flag was provided (e.g. --yes).
func (f *Flags) Bool(key string) bool { return f.set[key] }

func (f *Flags) Keys() []string {
	keys := make([]string, 0, len(f.set))
	for key := range f.set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
