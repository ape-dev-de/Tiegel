package phpstan

// PHP values as read from PHPStan's files without running PHP: the
// result of unserialize() (result cache 2.2+), of a var_export()ed literal
// (result cache before 2.2, PHP baselines) and of NEON (configuration).
// Values are nil, bool, int64, float64, string, *Array or *Object.

// Array is an ordered PHP array (also a NEON list or mapping).
type Array struct {
	Keys   []any // int64 or string
	Values []any
}

// Len returns the number of entries.
func (a *Array) Len() int {
	if a == nil {
		return 0
	}
	return len(a.Keys)
}

// Get returns the value of the string key k.
func (a *Array) Get(k string) (any, bool) {
	if a == nil {
		return nil, false
	}
	for i, key := range a.Keys {
		if s, ok := key.(string); ok && s == k {
			return a.Values[i], true
		}
	}
	return nil, false
}

// set appends or replaces key k.
func (a *Array) set(k, v any) {
	for i, key := range a.Keys {
		if key == k {
			a.Values[i] = v
			return
		}
	}
	a.Keys = append(a.Keys, k)
	a.Values = append(a.Values, v)
}

// add appends v with the next integer key.
func (a *Array) add(v any) {
	var next int64
	for _, k := range a.Keys {
		if n, ok := k.(int64); ok && n >= next {
			next = n + 1
		}
	}
	a.Keys = append(a.Keys, next)
	a.Values = append(a.Values, v)
}

// Object is a PHP object; property names are unmangled (private and
// protected markers removed).
type Object struct {
	Class string
	Props *Array
}

// Get returns a property.
func (o *Object) Get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	return o.Props.Get(k)
}

// field returns the property or array entry k of v.
func field(v any, k string) any {
	switch t := v.(type) {
	case *Object:
		x, _ := t.Get(k)
		return x
	case *Array:
		x, _ := t.Get(k)
		return x
	}
	return nil
}

// str returns v as string ("" for others).
func str(v any) string {
	s, _ := v.(string)
	return s
}

// num returns v as int (0 for others).
func num(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

// truthy reports a PHP-true bool.
func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}
