package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

type t byte

// TODO ensure matches doc
const (
	exitt  = t(0b000)
	entert = t(0b001)
	recvt  = t(0b010)
	sendt  = t(0b011)
	errt   = t(0b11111111)
)

type message struct {
	t t      // type
	p uint32 // port
	b string // body
}

func client() {
	conn, err := net.Dial("tcp", "localhost:1703")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	outgoing := make(chan message)
	defer close(outgoing)

	messages := make(chan message)

	go writer(conn, outgoing)
	go reader(conn, messages)

	go func() {
		for m := range messages {
			switch m.t {
			case recvt:
				fmt.Printf("< %d ? %s\n", m.p, m.b)
			case errt:
				fmt.Printf("ERR %s\n", m.b)
			}
		}
	}()

	s := bufio.NewScanner(os.Stdin)
loop:
	for {
		fmt.Print("> ")
		if !s.Scan() {
			break
		}
		cmd := s.Text()
		if len(cmd) == 0 {
			fmt.Println("invalid command")
			continue
		}
		if strings.HasSuffix(cmd, "XXX") {
			fmt.Println("command cancelled")
			continue
		}
		var port uint32
		var body string
		switch {
		case send(cmd, &port, &body):
			outgoing <- message{sendt, port, body}
		case enter(cmd, &port):
			outgoing <- message{entert, port, ""}
		case exit(cmd, &port):
			outgoing <- message{exitt, port, ""}
		case quit(cmd):
			fmt.Println("quitting. goodbye!")
			break loop
		default:
			fmt.Println("invalid command")
		}
	}
	if err := s.Err(); err != nil {
		log.Fatal(err)
	}
}

func main() {
	client()
}

func send(cmd string, portp *uint32, messagep *string) bool {
	ports, cmd, ok := strings.Cut(cmd, " ")
	if !ok {
		return false
	}
	bang, cmd, ok := strings.Cut(cmd, " ")
	if !ok {
		return false
	}
	if bang != "!" {
		return false
	}
	port, err := strconv.ParseUint(ports, 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	*messagep = cmd
	return true
}

func enter(cmd string, portp *uint32) bool {
	bang, ports, ok := strings.Cut(cmd, " ")
	if !ok {
		return false
	}
	if bang != "!" {
		return false
	}
	port, err := strconv.ParseUint(ports, 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	return true
}

func exit(cmd string, portp *uint32) bool {
	dot, ports, ok := strings.Cut(cmd, " ")
	if !ok {
		return false
	}
	if dot != "." {
		return false
	}
	port, err := strconv.ParseUint(ports, 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	return true
}

func quit(cmd string) bool {
	return cmd == "q"
}

func writer(w io.Writer, outgoing <-chan message) {
	for c := range outgoing {
		if _, err := w.Write([]byte{byte(c.t)}); err != nil {
			log.Println(err)
			break
		}
		if err := binary.Write(w, binary.LittleEndian, c.p); err != nil {
			log.Println(err)
			break
		}
		if c.t == sendt {
			if _, err := io.WriteString(w, c.b); err != nil {
				log.Println(err)
				break
			}
		}
	}

	// drain
	for range outgoing {
	}
}

// TODO redo according to server
func reader(r io.Reader, messages chan<- message) {
	defer close(messages)
	var buf [256]byte

	// TODO handle messages that span more than 1 r.Read()

	// maybe model as state machine

	for {
		n, err := r.Read(buf[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			break
		}
		bs := buf[:n]
		t, bs := t(bs[0]), bs[1:]

		switch t {
		case recvt:
			var port uint32
			br := bytes.NewReader(bs[1:4]) // hopefully a non-power-of-two number of bytes isn't a problem
			if err := binary.Read(br, binary.LittleEndian, &port); err != nil {
				log.Println(err)
				continue
			}
			messages <- message{recvt, port, string(bs[4:])}
		case errt:
			// for now just return the error code as a string
			messages <- message{errt, 0, strconv.Itoa(int(bs[1]))}
		default:
			log.Printf("weird message type %b\n", t)
		}
	}
}
