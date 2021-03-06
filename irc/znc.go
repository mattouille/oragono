// Copyright (c) 2019 Shivaram Lingamneni <slingamn@cs.stanford.edu>
// released under the MIT license

package irc

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type zncCommandHandler func(client *Client, command string, params []string, rb *ResponseBuffer)

var zncHandlers = map[string]zncCommandHandler{
	"*playback": zncPlaybackHandler,
}

func zncPrivmsgHandler(client *Client, command string, privmsg string, rb *ResponseBuffer) {
	zncModuleHandler(client, command, strings.Fields(privmsg), rb)
}

func zncModuleHandler(client *Client, command string, params []string, rb *ResponseBuffer) {
	command = strings.ToLower(command)
	if subHandler, ok := zncHandlers[command]; ok {
		subHandler(client, command, params, rb)
	} else {
		nick := rb.target.Nick()
		rb.Add(nil, client.server.name, "NOTICE", nick, fmt.Sprintf(client.t("Oragono does not emulate the ZNC module %s"), command))
		rb.Add(nil, "*status!znc@znc.in", "NOTICE", nick, fmt.Sprintf(client.t("No such module [%s]"), command))
	}
}

// "number of seconds (floating point for millisecond precision) elapsed since January 1, 1970"
func zncWireTimeToTime(str string) (result time.Time) {
	var secondsPortion, fracPortion string
	dot := strings.IndexByte(str, '.')
	if dot == -1 {
		secondsPortion = str
	} else {
		secondsPortion = str[:dot]
		fracPortion = str[dot:]
	}
	seconds, _ := strconv.ParseInt(secondsPortion, 10, 64)
	fraction, _ := strconv.ParseFloat(fracPortion, 64)
	return time.Unix(seconds, int64(fraction*1000000000))
}

type zncPlaybackTimes struct {
	after   time.Time
	before  time.Time
	targets map[string]bool // nil for "*" (everything), otherwise the channel names
}

// https://wiki.znc.in/Playback
// PRIVMSG *playback :play <target> [lower_bound] [upper_bound]
// e.g., PRIVMSG *playback :play * 1558374442
func zncPlaybackHandler(client *Client, command string, params []string, rb *ResponseBuffer) {
	if len(params) < 2 {
		return
	} else if strings.ToLower(params[0]) != "play" {
		return
	}
	targetString := params[1]

	var after, before time.Time
	if 2 < len(params) {
		after = zncWireTimeToTime(params[2])
	}
	if 3 < len(params) {
		before = zncWireTimeToTime(params[3])
	}

	var targets map[string]bool

	// three cases:
	// 1. the user's PMs get played back immediately upon receiving this
	// 2. if this is a new connection (from the server's POV), save the information
	// and use it to process subsequent joins
	// 3. if this is a reattach (from the server's POV), immediately play back
	// history for channels that the client is already joined to. In this scenario,
	// there are three total attempts to play the history:
	//     3.1. During the initial reattach (no-op because the *playback privmsg
	//          hasn't been received yet, but they negotiated the znc.in/playback
	//          cap so we know we're going to receive it later)
	//     3.2  Upon receiving the *playback privmsg, i.e., now: we should play
	//          the relevant history lines
	//     3.3  When the client sends a subsequent redundant JOIN line for those
	//          channels; redundant JOIN is a complete no-op so we won't replay twice

	config := client.server.Config()
	if params[1] == "*" {
		items, _ := client.history.Between(after, before, false, config.History.ChathistoryMax)
		client.replayPrivmsgHistory(rb, items, true)
	} else {
		for _, targetName := range strings.Split(targetString, ",") {
			if cfTarget, err := CasefoldChannel(targetName); err == nil {
				if targets == nil {
					targets = make(map[string]bool)
				}
				targets[cfTarget] = true
			}
		}
	}

	rb.session.zncPlaybackTimes = &zncPlaybackTimes{
		after:   after,
		before:  before,
		targets: targets,
	}

	for _, channel := range client.Channels() {
		channel.autoReplayHistory(client, rb, "")
		rb.Flush(true)
	}
}
