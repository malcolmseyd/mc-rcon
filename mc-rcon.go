package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/howeyc/gopass"
)

// RESPONSETYPE is for responses to commands
const RESPONSETYPE = 0

// COMMANDTYPE is for sending commands and receiving a login response
const COMMANDTYPE = 2

// LOGINTYPE is for sending login requests
const LOGINTYPE = 3

// TIMEOUT is for dialing + sending/receiving packets
const TIMEOUT = 5 * time.Second

// MAXRECVSIZE is 12 + 4096 + 2 bytes
const MAXRECVSIZE = 4110

// prints to stderr and exits
func ferrorln(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
}

// prints to stderr
func errorln(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

func colorPrint(text string, colored bool) {
	colorMap := map[byte]color.Attribute{
		'0': color.FgBlack,
		'1': color.FgBlue,
		'2': color.FgGreen,
		'3': color.FgCyan,
		'4': color.FgRed,
		'5': color.FgMagenta,
		'6': color.FgYellow,
		'7': color.FgWhite,
		'8': color.FgHiBlack,
		'9': color.FgHiBlue,
		'a': color.FgHiGreen,
		'b': color.FgHiCyan,
		'c': color.FgHiRed,
		'd': color.FgHiMagenta,
		'e': color.FgHiYellow,
		'f': color.FgHiWhite,
	}

	// clean up extra newlines
	text = strings.Trim(text, "\n")

	if !strings.Contains(text, "§") {
		fmt.Println(text)
		return
	}

	textParts := strings.Split(text, "§")
	for i, part := range textParts {
		// check if first one is color coded
		if i == 0 && text[0] != '§' {
			fmt.Print(part)
			continue
		}
		if colored {
			color.Set(colorMap[part[0]])
		}
		fmt.Print(part[1:])
		color.Unset()
	}

	fmt.Println()
}

type rconPacket struct {
	requestID  int32
	packetType int32
	payload    string
}

func (rp *rconPacket) serialize() []byte {

	// we keep the first 4 bytes for the packet size
	packet := bytes.NewBuffer([]byte{})

	// size = requestID + packetType + payload + padding
	packetSize := int32(4 + 4 + len(rp.payload) + 2)

	// integers are little endian, opposite of Minecraft protocol
	binary.Write(packet, binary.LittleEndian, packetSize)
	binary.Write(packet, binary.LittleEndian, rp.requestID)
	binary.Write(packet, binary.LittleEndian, rp.packetType)
	binary.Write(packet, binary.LittleEndian, []byte(rp.payload))

	// two bytes of padding at the end
	packet.Write([]byte{0, 0})

	return packet.Bytes()
}

func parsePacket(data []byte) rconPacket {
	var packetSize int32
	reader := bytes.NewReader(data)
	packet := rconPacket{}
	binary.Read(reader, binary.LittleEndian, &packetSize)
	binary.Read(reader, binary.LittleEndian, &packet.requestID)
	binary.Read(reader, binary.LittleEndian, &packet.packetType)

	// packetSize is actual size-4, so end-2 is packetSize+2
	packet.payload = string(data[12 : packetSize+2])

	return packet
}

func makeLoginPacket(password string, requestID int32) []byte {
	packet := rconPacket{
		requestID:  requestID,
		packetType: LOGINTYPE,
		payload:    password,
	}
	return packet.serialize()
}

func makeCommandPacket(command string, requestID int32) []byte {
	packet := rconPacket{
		requestID:  requestID,
		packetType: COMMANDTYPE,
		payload:    command,
	}
	return packet.serialize()
}

func sendPacket(packet []byte, conn *net.TCPConn) error {
	conn.SetWriteDeadline(time.Now().Add(TIMEOUT))
	_, err := conn.Write(packet)
	if err != nil {
		return err
	}

	// log.Println("Packet sent")
	return nil
}

func receivePacket(conn *net.TCPConn) ([]byte, error) {
	received := bytes.NewBuffer([]byte{})
	buf := make([]byte, MAXRECVSIZE)
	for {
		conn.SetReadDeadline(time.Now().Add(TIMEOUT))
		n, err := conn.Read(buf)
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			return nil, err
		}

		received.Write(buf[:n])
		// not great but it's very fast and fragmentation is rare
		// a more consistent approach could be send a second packet with an invalid type
		// like 999 and wait for a payload that contains "Unknown request 100"
		// unfortunately, there is no "correct" way to deal with fragmentation
		if n < len(buf) {
			break
		}
	}

	// log.Println("Packet Recieved,", received.Len(), "bytes")
	return received.Bytes(), nil
}

func connect(host, port string) (*net.TCPConn, error) {
	conn, err := net.DialTimeout("tcp", host+":"+port, TIMEOUT)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil

}

func login(password string, requestID int32, conn *net.TCPConn) error {
	loginPacket := makeLoginPacket(password, requestID)
	// fmt.Println("Login packet:\n", hex.Dump(loginPacket))

	err := sendPacket(loginPacket, conn)
	if err != nil {
		return err
	}

	response, err := receivePacket(conn)
	if err != nil {
		return err
	}
	// fmt.Println("Response packet:\n", hex.Dump(response))

	parsedResponse := parsePacket(response)
	// fmt.Println("Parsed:\n", parsedResponse)

	if parsedResponse.requestID == requestID && parsedResponse.packetType == COMMANDTYPE {
		return nil
	} else if parsedResponse.requestID == -1 {
		return fmt.Errorf("Password is incorrect")
	}

	// shouldn't get here
	return fmt.Errorf("Login failed")
}

// return nil to quit program
func interactiveMode(requestID int32, conn *net.TCPConn, colored bool) error {
	errchan := make(chan error, 1)

	// handle ctrl+c and other kill methods
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, os.Interrupt, os.Kill)
	go func() {
		<-sigchan
		errchan <- nil
		return
	}()

	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("> ")
			input, err := reader.ReadString('\n')
			// handle ctrl+d
			if err == io.EOF {
				fmt.Print("^D")
				errchan <- nil
				return
			}
			if err != nil {
				errchan <- err
				return
			}

			commandPacket := makeCommandPacket(strings.TrimSpace(input), requestID)
			err = sendPacket(commandPacket, conn)
			if err != nil {
				errchan <- err
				return
			}

			responsePacket, err := receivePacket(conn)
			if len(responsePacket) == 0 {
				errchan <- fmt.Errorf("No response received")
				return
			}
			response := parsePacket(responsePacket)
			colorPrint(response.payload, colored)
			color.Unset()
		}
	}()

	return <-errchan
}

func printHelp() {
	fmt.Println("Usage: mc_rcon [OPTIONS...] HOST")
	flag.PrintDefaults()
}

func main() {
	var host string
	var port string
	var password string
	var noColor bool

	flag.StringVar(&port, "port", "25575", "port number")
	flag.BoolVar(&noColor, "no-color", false, "disable color output")

	flag.Parse()

	if len(flag.Args()) != 1 {
		printHelp()
		os.Exit(1)
	}

	host = flag.Arg(0)

	// generate a random id to use for the session
	requestID := int32(rand.Int())

	connection, err := connect(host, port)
	if err != nil {
		ferrorln(err)
	}

	// ask for passwork since connection is successful
	bytePassword, err := gopass.GetPasswdPrompt("Password: ", false, os.Stdin, os.Stdout)
	if err != nil {
		ferrorln(err)
	}
	password = string(bytePassword)

	err = login(password, requestID, connection)
	if err != nil {
		ferrorln(err)
	}
	fmt.Println("Successfully logged in")

	err = interactiveMode(requestID, connection, !noColor)
	if err != nil {
		errorln(err)
	} else {
		fmt.Println("\nShutting down...")
	}

	err = connection.Close()
	if err != nil {
		errorln(err)
	}
	// fmt.Println("Connection closed")
}
