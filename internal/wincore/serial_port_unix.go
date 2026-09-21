//go:build !windows

package wincore

import "io"

func openVirtualCOM(string) (io.ReadWriteCloser, bool, error) { return nil, false, nil }
func appendVirtualCOMPorts(ports []string) ([]string, error)  { return ports, nil }
