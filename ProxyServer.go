package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/z-ken/media-proxy-server/udp"
)

func handleFrontendInbound(conn net.Conn, inBoundChannel chan string, outBoundChannel chan string,
	mappedAddrChannel chan []string) {

	defer conn.Close()

	bufferReader := bufio.NewReader(conn)

	var originAddr = ""

	for {
		requestLine, err := bufferReader.ReadString('\n')
		if err != nil {
			log.Println("\n>>> Client connection closed: ", conn.RemoteAddr().String())
			inBoundChannel <- "CLOSE"
			outBoundChannel <- "CLOSE"
			return
		}

		fmt.Print("[ Original Inbound ]  " + requestLine)

		tokens := strings.Split(requestLine, " ")

		if tokens[0] == "OPTIONS" {

			proxyAddr := tokens[1]
			proxyURL, _ := url.Parse(proxyAddr)

			originAddr = proxyMapping[proxyURL.Path]
			if originAddr == "" {
				log.Printf("Can't find original Address to [%s]", proxyAddr)
				mappedAddrChannel <- []string{proxyAddr, "NOT_FOUND"}
				return
			}

			// Inform other handler originAddr is mapped successfully
			mappedAddrChannel <- []string{proxyAddr, originAddr}

		} else if tokens[0] == "DESCRIBE" {

			tokens[1] = originAddr
		}

		requestLine = strings.Join(tokens, " ")

		inBoundChannel <- requestLine
	}
}

func handleBackendInBound(proxyConn net.Conn, inBoundChannel chan string, clientConn net.Conn) {

	for {
		requestLine := <- inBoundChannel

		if requestLine == "CLOSE" {
			return
		}

		if strings.Contains(requestLine, "Transport: RTP/AVP/UDP") {
			requestLine = strings.Trim(requestLine, "\r\n")
			transParts := strings.Split(requestLine, ";")

			for idx, part := range transParts {

				if strings.Contains(part, "client_port") {
					minusPos := strings.Index(part, "-")

					fromPortStr := part[12:minusPos]
					fromPort,_ := strconv.Atoi(fromPortStr)
					toPort,_ := strconv.Atoi(part[minusPos+1:])

					newFromPort := strconv.Itoa(fromPort + 5)
					newToPort := strconv.Itoa(toPort + 5)
					transParts[idx] = "client_port=" + newFromPort + "-" + newToPort

					clientAddr := clientConn.RemoteAddr().String()
					clientAddrParts := strings.Split(clientAddr, ":")

					proxyLocalAddr := proxyConn.LocalAddr().String()
					proxyLocalAddrParts := strings.Split(proxyLocalAddr, ":")

					addrPair <- udp.AddrPair{
						ClientAddr: clientAddrParts[0] + ":" + fromPortStr,
						ProxyAddr: proxyLocalAddrParts[0] + ":" + newFromPort }

					break
				}
			}

			requestLine = strings.Join(transParts, ";")
		}

		_, err := io.WriteString(proxyConn, requestLine)
		if err != nil {
			log.Println(err)
		}
	}
}

func handleFrontendOutbound(conn net.Conn, outBoundChannel chan string, proxyAddr string) {

	var redirect bool

	for {
		respStr := <- outBoundChannel

		if respStr == "CLOSE" {
			return
		}

		if redirect {

			location := strings.Index(respStr, "Location: ")
			if location >= 0 {

				respStr = strings.Trim(respStr, "\r\n")

				redirectOriginUrl := respStr[location+10:]

				redirectUrl := proxyAddr + "_redirect"

				redirectURL, _ := url.Parse(redirectUrl)

				proxyMapping[redirectURL.Path] = redirectOriginUrl

				respStr = "Location: " + redirectUrl +"\r\n"

				redirect = false
			}
		}

		if strings.Contains(respStr, "RTSP/1.0 302 Moved Temporarily") {
			redirect = true
		}

		contentBase := strings.Index(respStr, "Content-Base: ")
		if contentBase >= 0 {
			respStr = "Content-Base: " + proxyAddr + "\r\n"
		}

		_, err := io.WriteString(conn, respStr)
		if err != nil {
			log.Println(err)
		}
	}
}

func handleBackendOutBound(proxyConn net.Conn, outBoundChannel chan string, inBoundChannel chan string) {

	defer proxyConn.Close()

	bufferReader := bufio.NewReader(proxyConn)

	for {
		respLine, err := bufferReader.ReadString('\n')
		if err != nil {
			log.Println("\n>>> Proxy connection closed: " + proxyConn.LocalAddr().String())
			inBoundChannel <- "CLOSE"
			outBoundChannel <- "CLOSE"
			return
		}

		fmt.Print("[ Proxy Outbound ]  " + respLine)

		outBoundChannel <- respLine
	}
}

func getProxyConn(backUrl string) (proxyConn net.Conn, success bool) {

	proxyURL, _ := url.Parse(backUrl)

	proxyConn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		log.Println("Proxy connection failed: ", err.Error())
		return proxyConn, false
	}

	return proxyConn, true
}

func handleConnection(conn net.Conn) {

	localAddr := conn.LocalAddr().String()
	log.Printf("\nNew Connection from [%s] to [%s]\n\n", conn.RemoteAddr().String(), localAddr)

	inBoundChannel := make(chan string)
	outBoundChannel := make(chan string)
	mappedAddrChannel := make(chan []string)

	go handleFrontendInbound(conn, inBoundChannel, outBoundChannel, mappedAddrChannel)

	mappedAddr := <- mappedAddrChannel
	proxyAddr := mappedAddr[0]
	originAddr := mappedAddr[1]

	if originAddr != "" && originAddr != "NOT_FOUND" {

		proxyConn, success := getProxyConn(originAddr)
		if success {

			go handleBackendInBound(proxyConn, inBoundChannel, conn)

			go handleFrontendOutbound(conn, outBoundChannel, proxyAddr)

			go handleBackendOutBound(proxyConn, outBoundChannel, inBoundChannel)

		}
	}
}


// ------------------------------------------------------------------- //


var (

	proxyPort = flag.String("p", "9955", "Proxy Port")

	proxyMapping = make(map[string]string)

	addrPair = make(chan udp.AddrPair)

	streamPair = flag.String("s", "", "[Proxy path-1]|[Origin stream url-1]||[Proxy path-2]|[Origin stream url-2]...")
)

func main() {

	flag.Parse()

	pairs := strings.Split(*streamPair, "||")
	if len(pairs) > 0 {
		for _, pair := range pairs {
			kv := strings.Split(pair, "|")
			if len(kv) > 1 {
				proxyMapping[kv[0]] = kv[1]
			}
		}
	}

	go udp.ConnFactory(addrPair)


	ln, err := net.Listen("tcp", ":" + *proxyPort)
	if err != nil {
		log.Println("Error listening on port: ", *proxyPort, err.Error())
		return
	}

	defer ln.Close()

	//main loop: accept connection
	for {
		conn, err := ln.Accept()

		if err != nil {
			log.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}

		go handleConnection(conn)
	}
}
