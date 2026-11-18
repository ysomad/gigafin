// Package useragent names the browser and system of an HTTP User-Agent, as lists of
// sessions show them, such as Chrome on macOS.
package useragent

import "strings"

// Agent is the browser and system a User-Agent names; either is empty when unknown.
type Agent struct {
	Browser string
	OS      string
}

// _browsers are the tokens of browsers in the order to test them: Chromium-based browsers
// also claim Chrome and Safari, and Chrome and Firefox on iOS claim Safari.
var _browsers = [][2]string{
	{"YaBrowser/", "Yandex Browser"},
	{"Edg", "Edge"},
	{"OPR/", "Opera"},
	{"Firefox/", "Firefox"},
	{"FxiOS/", "Firefox"},
	{"CriOS/", "Chrome"},
	{"Chrome/", "Chrome"},
	{"Safari/", "Safari"},
}

// _systems are the tokens of systems in the order to test them: an iPhone claims Mac OS X,
// Android claims Linux.
var _systems = [][2]string{
	{"iPhone", "iPhone"},
	{"iPad", "iPad"},
	{"Android", "Android"},
	{"Mac OS X", "macOS"},
	{"Windows", "Windows"},
	{"Linux", "Linux"},
}

// Parse returns the browser and system ua names.
func Parse(ua string) Agent {
	var a Agent
	for _, b := range _browsers {
		if strings.Contains(ua, b[0]) {
			a.Browser = b[1]
			break
		}
	}
	for _, s := range _systems {
		if strings.Contains(ua, s[0]) {
			a.OS = s[1]
			break
		}
	}
	return a
}
