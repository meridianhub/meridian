// export_test.go exposes internal functions for white-box testing.
package migrations

// BuildServiceDSN is the exported test alias for buildServiceDSN.
func BuildServiceDSN(superuserDSN string, sdb ServiceDatabase, driver Driver) string {
	return buildServiceDSN(superuserDSN, sdb, driver)
}

// BuildSuperuserDSN is the exported test alias for buildSuperuserDSN.
func BuildSuperuserDSN(superuserDSN string, dbName string, driver Driver) string {
	return buildSuperuserDSN(superuserDSN, dbName, driver)
}
