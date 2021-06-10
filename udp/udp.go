package udp

import (
	"fmt"
	"log"
	"net"
)

const (
	// UDP Package size
	PackageSize = 2048
)

type AddrPair struct {
	ClientAddr string
	ProxyAddr  string
}

func createUdpListening(pair AddrPair, udpChannel chan []byte) {

	proxyUDPAddr, _ := net.ResolveUDPAddr("udp", pair.ProxyAddr)

	udpConn, err := net.ListenUDP("udp", proxyUDPAddr)
	if err != nil {
		log.Println("Error accepting connection: ", err.Error())

	}

	defer udpConn.Close()

	for {

		data := make([]byte, PackageSize)

		n, _, err := udpConn.ReadFromUDP(data)
		if err != nil {
			fmt.Println("failed to read UDP from origin, because of ", err.Error())
			return
		}

		//fmt.Println(n, remoteAddr)

		udpChannel <- data[:n]
	}
}

func createUdpSending(pair AddrPair, udpChannel chan []byte) {

	clientUDPAddr, _ := net.ResolveUDPAddr("udp", pair.ClientAddr)

	udpConn, err := net.DialUDP("udp", nil, clientUDPAddr)
	if err != nil {
		log.Println("Error dialing connection: ", err.Error())
		return
	}

	defer udpConn.Close()

	for {

		data := <- udpChannel

		_, err := udpConn.Write(data)
		if err != nil {
			fmt.Println("failed to write UDP to client, because of ", err.Error())
			return
		}

		//fmt.Println("Proxied:  ", n)
	}
}

func ConnFactory(udpPair chan AddrPair) {

	for {
		pair := <- udpPair

		udpChannel := make(chan []byte)

		go createUdpListening(pair, udpChannel)
		go createUdpSending(pair, udpChannel)
	}
}