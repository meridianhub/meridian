// Package quantity defines the core interfaces for the Universal Asset System.
package quantity

import (
	"sync"
)

// kv represents a key-value pair stored in the attribute bag.
// Using a struct instead of map enables efficient pooling without allocation on unmarshal.
type kv struct {
	key   string
	value string
}

// AttributeBag is a poolable implementation of attribute storage.
// It uses a slice of key-value pairs instead of a map to enable efficient
// sync.Pool reuse without per-unmarshal allocations.
type AttributeBag struct {
	entries []kv
}

// attributeBagPool provides allocation pooling for AttributeBag instances.
// This reduces GC pressure at high TPS by reusing allocated memory.
var attributeBagPool = sync.Pool{
	New: func() any {
		return &AttributeBag{
			entries: make([]kv, 0, 16),
		}
	},
}

// AcquireAttributeBag retrieves an AttributeBag from the pool.
// The returned bag is empty and ready for use.
// Caller must call ReleaseAttributeBag when done.
func AcquireAttributeBag() *AttributeBag {
	bag, ok := attributeBagPool.Get().(*AttributeBag)
	if !ok {
		return &AttributeBag{entries: make([]kv, 0, 16)}
	}
	return bag
}

// ReleaseAttributeBag returns an AttributeBag to the pool for reuse.
// The bag is automatically reset before being returned.
// After calling this function, the bag must not be used.
func ReleaseAttributeBag(bag *AttributeBag) {
	if bag == nil {
		return
	}
	bag.Reset()
	attributeBagPool.Put(bag)
}

// Get retrieves the value for a key, returning empty string and false if not found.
func (b *AttributeBag) Get(key string) (string, bool) {
	for i := range b.entries {
		if b.entries[i].key == key {
			return b.entries[i].value, true
		}
	}
	return "", false
}

// Set adds or updates a key-value pair.
// If the key exists, its value is updated in place.
// If the key doesn't exist, a new entry is appended.
func (b *AttributeBag) Set(key, value string) {
	for i := range b.entries {
		if b.entries[i].key == key {
			b.entries[i].value = value
			return
		}
	}
	b.entries = append(b.entries, kv{key: key, value: value})
}

// Keys returns all keys in the bag.
// The order matches insertion order (with updates preserving position).
func (b *AttributeBag) Keys() []string {
	keys := make([]string, len(b.entries))
	for i := range b.entries {
		keys[i] = b.entries[i].key
	}
	return keys
}

// Len returns the number of entries in the bag.
func (b *AttributeBag) Len() int {
	return len(b.entries)
}

// Reset clears all entries from the bag for reuse.
// The underlying capacity is preserved to avoid reallocation.
func (b *AttributeBag) Reset() {
	// Clear references to allow GC of string values
	for i := range b.entries {
		b.entries[i].key = ""
		b.entries[i].value = ""
	}
	b.entries = b.entries[:0]
}

// ToMap creates a map representation of the bag.
// This is intended for CEL expression evaluation where map access is required.
// The returned map is a transient copy; modifications do not affect the bag.
func (b *AttributeBag) ToMap() map[string]string {
	m := make(map[string]string, len(b.entries))
	for i := range b.entries {
		m[b.entries[i].key] = b.entries[i].value
	}
	return m
}

// ToAttributes converts the bag to a slice of Attribute structs.
// This is useful for compatibility with the Quantity interface.
func (b *AttributeBag) ToAttributes() []Attribute {
	attrs := make([]Attribute, len(b.entries))
	for i := range b.entries {
		attrs[i] = Attribute{
			Key:   b.entries[i].key,
			Value: b.entries[i].value,
		}
	}
	return attrs
}

// FromAttributes populates the bag from a slice of Attribute structs.
// Any existing entries are preserved; duplicates are updated in place.
func (b *AttributeBag) FromAttributes(attrs []Attribute) {
	for _, attr := range attrs {
		b.Set(attr.Key, attr.Value)
	}
}
