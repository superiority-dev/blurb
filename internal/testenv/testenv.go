// Package testenv holds the environment helpers blurb's tests share.
package testenv

// Lookup returns a function with the signature of [os.LookupEnv] that finds
// the keys in kv, each followed by its value.
func Lookup(kv ...string) func(string) (string, bool) {
	env := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		env[kv[i]] = kv[i+1]
	}

	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}
