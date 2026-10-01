//go:build !windows

package core

import "io"

func openVirtualCOM(string) (io.ReadWriteCloser, bool, error) { return nil, false, nil }
func appendVirtualCOMPorts(ports []string) ([]string, error)  { return ports, nil }
