package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.bug.st/serial"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	list := flag.Bool("list", false, "列出可用串口")
	portName := flag.String("port", "", "串口名称，如 /dev/tty.usbserial-0001 或 COM3")
	baud := flag.Int("baud", 115200, "波特率")
	dataBits := flag.Int("data", 8, "数据位: 5/6/7/8")
	stopBits := flag.Int("stop", 1, "停止位: 1/2")
	parityName := flag.String("parity", "none", "校验: none/odd/even")
	hexView := flag.Bool("hex", false, "以 HEX 显示接收数据")
	hexSend := flag.Bool("hex-send", false, "将输入行作为 HEX 发送，如: 01 03 00 00 00 02")
	eol := flag.String("eol", "none", "文本发送行尾: none/lf/cr/crlf")
	flag.Parse()

	if *list {
		ports, err := serial.GetPortsList()
		if err != nil {
			return err
		}
		if len(ports) == 0 {
			fmt.Println("未发现串口")
			return nil
		}
		for _, name := range ports {
			fmt.Println(name)
		}
		return nil
	}
	if *portName == "" {
		return errors.New("请用 -port 指定串口，或用 -list 查看串口")
	}

	mode, err := makeMode(*baud, *dataBits, *stopBits, *parityName)
	if err != nil {
		return err
	}
	lineEnd, err := parseEOL(*eol)
	if err != nil {
		return err
	}
	port, err := serial.Open(*portName, mode)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", *portName, err)
	}
	defer port.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "已连接 %s @ %d，输入后回车发送，Ctrl+C 退出\n", *portName, *baud)

	errCh := make(chan error, 2)
	go receive(port, *hexView, errCh)
	go send(port, *hexSend, lineEnd, errCh)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func makeMode(baud, dataBits, stopBits int, parityName string) (*serial.Mode, error) {
	if baud <= 0 || dataBits < 5 || dataBits > 8 {
		return nil, errors.New("无效的波特率或数据位")
	}
	mode := &serial.Mode{BaudRate: baud, DataBits: dataBits}
	switch stopBits {
	case 1:
		mode.StopBits = serial.OneStopBit
	case 2:
		mode.StopBits = serial.TwoStopBits
	default:
		return nil, errors.New("停止位只能是 1 或 2")
	}
	switch strings.ToLower(parityName) {
	case "none":
		mode.Parity = serial.NoParity
	case "odd":
		mode.Parity = serial.OddParity
	case "even":
		mode.Parity = serial.EvenParity
	default:
		return nil, errors.New("校验只能是 none、odd 或 even")
	}
	return mode, nil
}

func parseEOL(name string) ([]byte, error) {
	switch strings.ToLower(name) {
	case "none":
		return nil, nil
	case "lf":
		return []byte{'\n'}, nil
	case "cr":
		return []byte{'\r'}, nil
	case "crlf":
		return []byte{'\r', '\n'}, nil
	default:
		return nil, errors.New("行尾只能是 none、lf、cr 或 crlf")
	}
}

func parseData(line string, asHex bool, eol []byte) ([]byte, error) {
	if asHex {
		clean := strings.Join(strings.Fields(line), "")
		data, err := hex.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("HEX 格式错误: %w", err)
		}
		return data, nil
	}
	return append([]byte(line), eol...), nil
}

func receive(port serial.Port, asHex bool, errCh chan<- error) {
	buf := make([]byte, 1024)
	for {
		n, err := port.Read(buf)
		if n > 0 {
			if asHex {
				fmt.Printf("% X\n", buf[:n])
			} else {
				fmt.Print(string(buf[:n]))
			}
		}
		if err != nil {
			errCh <- fmt.Errorf("读取串口: %w", err)
			return
		}
	}
}

func send(port serial.Port, asHex bool, eol []byte, errCh chan<- error) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		data, err := parseData(scanner.Text(), asHex, eol)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if len(data) == 0 {
			continue
		}
		if _, err := port.Write(data); err != nil {
			errCh <- fmt.Errorf("写入串口: %w", err)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		errCh <- fmt.Errorf("读取输入: %w", err)
	} else {
		errCh <- io.EOF
	}
}
