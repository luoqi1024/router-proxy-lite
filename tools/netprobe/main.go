// netprobe is an optional lab utility, never included in router installation bundles.
// It tests UDP DNS and a STUN request through SOCKS5 without printing exit addresses.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	dns := flag.String("dns", "192.168.31.1:1053", "DNS listener to test")
	socks := flag.String("socks", "127.0.0.1:17890", "SOCKS5 listener")
	stun := flag.String("stun", "stun.cloudflare.com:3478", "STUN endpoint")
	flag.Parse()
	if err := probeDNS(*dns); err != nil {
		fmt.Fprintln(os.Stderr, "UDP DNS:", err)
		os.Exit(1)
	}
	fmt.Println("UDP DNS: valid response")
	if err := probeSTUN(*socks, *stun); err != nil {
		fmt.Fprintln(os.Stderr, "SOCKS5 UDP/STUN:", err)
		os.Exit(1)
	}
	fmt.Println("SOCKS5 UDP/STUN: matching binding success response")
}
func probeDNS(address string) error {
	c, err := net.DialTimeout("udp", address, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	query := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 5, 'b', 'a', 'i', 'd', 'u', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	if _, err = rand.Read(query[:2]); err != nil {
		return err
	}
	if _, err = c.Write(query); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, err := c.Read(response)
	if err != nil {
		return err
	}
	if n < 12 || !bytes.Equal(query[:2], response[:2]) || response[2]&128 == 0 || response[3]&15 != 0 || binary.BigEndian.Uint16(response[6:8]) == 0 {
		return fmt.Errorf("invalid DNS response")
	}
	return nil
}
func readAddress(r io.Reader) (string, error) {
	typ := []byte{0}
	if _, err := io.ReadFull(r, typ); err != nil {
		return "", err
	}
	var host string
	switch typ[0] {
	case 1, 4:
		size := 4
		if typ[0] == 4 {
			size = 16
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 3:
		if _, err := io.ReadFull(r, typ); err != nil {
			return "", err
		}
		b := make([]byte, int(typ[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = string(b)
	default:
		return "", fmt.Errorf("invalid address type")
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(r, port); err != nil {
		return "", err
	}
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}
func probeSTUN(socks, server string) error {
	tcp, err := net.DialTimeout("tcp", socks, 5*time.Second)
	if err != nil {
		return err
	}
	defer tcp.Close()
	_ = tcp.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err = tcp.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(tcp, reply); err != nil {
		return err
	}
	if !bytes.Equal(reply, []byte{5, 0}) {
		return fmt.Errorf("SOCKS5 authentication rejected")
	}
	if _, err = tcp.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	header := make([]byte, 3)
	if _, err = io.ReadFull(tcp, header); err != nil {
		return err
	}
	if header[0] != 5 || header[1] != 0 {
		return fmt.Errorf("UDP associate rejected")
	}
	relay, err := readAddress(tcp)
	if err != nil {
		return err
	}
	udp, err := net.DialTimeout("udp", relay, 5*time.Second)
	if err != nil {
		return err
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(12 * time.Second))
	host, portText, err := net.SplitHostPort(server)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || len(host) > 255 {
		return fmt.Errorf("invalid STUN address")
	}
	frame := []byte{0, 0, 0, 3, byte(len(host))}
	frame = append(frame, host...)
	frame = append(frame, byte(port>>8), byte(port))
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request, 1)
	copy(request[4:8], []byte{0x21, 0x12, 0xa4, 0x42})
	if _, err = rand.Read(request[8:]); err != nil {
		return err
	}
	frame = append(frame, request...)
	if _, err = udp.Write(frame); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, err := udp.Read(response)
	if err != nil {
		return err
	}
	if n < 4 || response[2] != 0 {
		return fmt.Errorf("invalid UDP encapsulation")
	}
	reader := bytes.NewReader(response[3:n])
	if _, err = readAddress(reader); err != nil {
		return err
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(payload) < 20 || binary.BigEndian.Uint16(payload) != 0x0101 || !bytes.Equal(payload[4:20], request[4:20]) {
		return fmt.Errorf("invalid STUN transaction")
	}
	return nil
}
