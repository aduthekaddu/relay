//go:build !linux

package files

// renameNoReplace renames from → to unless to exists. On platforms
// without an atomic no-replace rename the check and the rename are two
// steps.
func renameNoReplace(from, to string) error { return renameCheck(from, to) }
