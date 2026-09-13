package wincore

// SendInputToConnection preserves history for targeted sends just as Send does.
func (e *Engine) SendInputToConnection(id, input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	if err = e.SendToConnection(id, data); err != nil {
		return err
	}
	e.rememberSend(input)
	return nil
}

func (e *Engine) SendInputToUDP(address, input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	if err = e.SendToUDP(address, data); err != nil {
		return err
	}
	e.rememberSend(input)
	return nil
}
