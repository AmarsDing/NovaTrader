package serial

import (
	"strconv"

	"go.bug.st/serial"
)

type Serial struct {
	PortName  string
	BaudRate  string
	DataBits  string
	StopBits  string
	Parity    string
	SendCount int64
	RecvCount int64
	Timeout   int
	Port      serial.Port
	mode      *serial.Mode
}

func NewSerial(portName string, baudRate string, dataBits string, stopBits string, parity string, timeout int) (*Serial, error) {
	s := &Serial{
		PortName:  portName,
		BaudRate:  baudRate,
		DataBits:  dataBits,
		StopBits:  stopBits,
		SendCount: 0,
		RecvCount: 0,
		Parity:    parity,
		Timeout:   timeout,
	}
	err := s.setMode()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Serial) Open() error {
	port, err := serial.Open(s.PortName, s.mode)
	if err != nil {
		return err
	}
	s.Port = port
	return nil
}

func (s *Serial) Close() error {
	return s.Port.Close()
}

func (s *Serial) Read() ([]byte, error) {
	for {
		buf := make([]byte, 1024)
		n, err := s.Port.Read(buf)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			s.RecvCount += int64(n)
			return buf[:n], nil
		}
	}
}

func (s *Serial) Write(data []byte) error {
	n, err := s.Port.Write(data)
	if err != nil {
		return err
	}
	s.SendCount += int64(n)
	return nil
}

func (s *Serial) SetTimeout(timeout int) error {
	s.Timeout = timeout
	return s.Port.SetReadTimeout(-1)
}

func (s *Serial) SetBaudRate(baudRate string) error {

	baud, err := strconv.Atoi(baudRate)
	if err != nil {
		return err
	}
	s.mode.BaudRate = baud
	return nil
}

// Size of the character (must be 5, 6, 7 or 8)
func (s *Serial) SetDataBits(dataBits string) error {
	switch dataBits {
	case "5":
		s.mode.DataBits = 5
	case "6":
		s.mode.DataBits = 6
	case "7":
		s.mode.DataBits = 7
	case "8":
		s.mode.DataBits = 8
	default:
		s.mode.DataBits = 8
	}
	s.DataBits = dataBits
	return nil
}

func (s *Serial) SetStopBits(stopBits string) error {
	switch stopBits {
	case "1":
		s.mode.StopBits = serial.OneStopBit
	case "1.5":
		s.mode.StopBits = serial.OnePointFiveStopBits
	case "2":
		s.mode.StopBits = serial.TwoStopBits
	default:
		s.mode.StopBits = serial.OneStopBit
	}
	s.StopBits = stopBits
	return nil
}

func (s *Serial) SetParity(parity string) error {
	switch parity {
	case "N":
		s.mode.Parity = serial.NoParity
	case "E":
		s.mode.Parity = serial.EvenParity
	case "O":
		s.mode.Parity = serial.OddParity
	case "M":
		s.mode.Parity = serial.MarkParity
	case "S":
		s.mode.Parity = serial.SpaceParity
	default:
		s.mode.Parity = serial.NoParity
	}
	s.Parity = parity
	return nil
}

func (s *Serial) SetPortName(portName string) error {
	s.PortName = portName
	return nil
}

func (s *Serial) setMode() error {
	err := s.SetBaudRate(s.BaudRate)
	if err != nil {
		return err
	}
	err = s.SetDataBits(s.DataBits)
	if err != nil {
		return err
	}
	err = s.SetParity(s.Parity)
	if err != nil {
		return err
	}
	err = s.SetParity(s.Parity)
	if err != nil {
		return err
	}
	err = s.SetStopBits(s.StopBits)
	if err != nil {
		return err
	}
	err = s.SetTimeout(s.Timeout)
	if err != nil {
		return err
	}
	return nil
}
