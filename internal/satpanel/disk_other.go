//go:build !linux

package satpanel

func diskSpace(string) (total, free uint64) { return 0, 0 }
