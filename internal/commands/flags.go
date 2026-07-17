package commands

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

// Bool reports a flag given without a value (e.g. --yes).
func (f *Flags) Bool(key string) bool { return f.set[key] }
