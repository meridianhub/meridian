package redislock

import "time"

// Config holds configuration for Redis distributed locks.
type Config struct {
	// KeyPrefix is prepended to lock keys.
	// For leader election, this is the full lock key.
	// For per-resource locks, keys are formatted as "{KeyPrefix}:{tenantID}:{resourceID}".
	KeyPrefix string
	// LockTTL is how long the lock is held before expiring.
	// Default: 5 minutes.
	LockTTL time.Duration
	// RenewEvery is how often to renew the lock.
	// Must be less than LockTTL. Default: 30 seconds.
	RenewEvery time.Duration
}

func (c Config) withDefaults() Config {
	if c.LockTTL <= 0 {
		c.LockTTL = 5 * time.Minute
	}
	if c.RenewEvery <= 0 {
		c.RenewEvery = 30 * time.Second
	}
	if c.RenewEvery >= c.LockTTL {
		c.RenewEvery = c.LockTTL / 2
	}
	if c.RenewEvery <= 0 {
		c.RenewEvery = time.Second
	}
	return c
}
