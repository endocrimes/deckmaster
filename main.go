package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/bendahl/uinput"
	"github.com/davecgh/go-spew/spew"
	"github.com/godbus/dbus"
	"github.com/muesli/streamdeck"
)

var (
	dev      streamdeck.Device
	dbusConn *dbus.Conn
	keyboard uinput.Keyboard
	x        Xorg

	deck          *Deck
	recentWindows []Window

	deckFile   = flag.String("deck", "deckmaster.deck", "path to deck config file")
	brightness = flag.Uint("brightness", 80, "brightness in percent")
)

func handleActiveWindowChanged(dev streamdeck.Device, event ActiveWindowChangedEvent) {
	fmt.Println(fmt.Sprintf("Active window changed to %s (%d, %s)",
		event.Window.Class, event.Window.ID, event.Window.Name))

	// remove dupes
	i := 0
	for _, rw := range recentWindows {
		if rw.ID == event.Window.ID {
			continue
		}

		recentWindows[i] = rw
		i++
	}
	recentWindows = recentWindows[:i]

	recentWindows = append([]Window{event.Window}, recentWindows...)
	if len(recentWindows) > 15 {
		recentWindows = recentWindows[0:15]
	}
	deck.updateWidgets()
}

func handleWindowClosed(dev streamdeck.Device, event WindowClosedEvent) {
	i := 0
	for _, rw := range recentWindows {
		if rw.ID == event.Window.ID {
			continue
		}

		recentWindows[i] = rw
		i++
	}
	recentWindows = recentWindows[:i]
	deck.updateWidgets()
}

func main() {
	flag.Parse()

	var err error
	deck, err = LoadDeck(*deckFile)
	if err != nil {
		log.Fatal(err)
	}

	dbusConn, err = dbus.SessionBus()
	if err != nil {
		panic(err)
	}

	x = Connect(os.Getenv("DISPLAY"))
	defer x.Close()

	tch := make(chan interface{})
	x.TrackWindows(tch, time.Second)

	d, err := streamdeck.Devices()
	if err != nil {
		log.Fatal(err)
	}
	if len(d) == 0 {
		fmt.Println("No Stream Deck devices found.")
		return
	}
	dev = d[0]

	err = dev.Open()
	if err != nil {
		log.Fatal(err)
	}
	ver, err := dev.FirmwareVersion()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found device with serial %s (firmware %s)\n",
		dev.Serial, ver)

	err = dev.Reset()
	if err != nil {
		log.Fatal(err)
	}

	if *brightness > 100 {
		*brightness = 100
	}
	err = dev.SetBrightness(uint8(*brightness))
	if err != nil {
		log.Fatal(err)
	}

	keyboard, err = uinput.CreateKeyboard("/dev/uinput", []byte("Deckmaster"))
	if err != nil {
		log.Printf("Could not create virtual input device (/dev/uinput): %s", err)
		log.Println("Emulating keyboard events will be disabled!")
	} else {
		defer keyboard.Close()
	}

	var keyStates sync.Map
	keyTimestamps := make(map[uint8]time.Time)

	kch, err := dev.ReadKeys()
	if err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case <-time.After(900 * time.Millisecond):
			deck.updateWidgets()

		case k, ok := <-kch:
			if !ok {
				err = dev.Open()
				if err != nil {
					log.Fatal(err)
				}
				continue
			}
			spew.Dump(k)

			var state bool
			if ks, ok := keyStates.Load(k.Index); ok {
				state = ks.(bool)
			}
			// fmt.Println("Storing state", k.Pressed)
			keyStates.Store(k.Index, k.Pressed)

			if state && !k.Pressed {
				// key was released
				if time.Since(keyTimestamps[k.Index]) < 200*time.Millisecond {
					fmt.Println("Triggering short action")
					deck.triggerAction(k.Index, false)
				}
			}
			if !state && k.Pressed {
				// key was pressed
				go func() {
					// launch timer to observe keystate
					time.Sleep(200 * time.Millisecond)

					if state, ok := keyStates.Load(k.Index); ok && state.(bool) {
						// key still pressed
						fmt.Println("Triggering long action")
						deck.triggerAction(k.Index, true)
					}
				}()
			}
			keyTimestamps[k.Index] = time.Now()

		case e := <-tch:
			switch event := e.(type) {
			case WindowClosedEvent:
				handleWindowClosed(dev, event)

			case ActiveWindowChangedEvent:
				handleActiveWindowChanged(dev, event)
			}
		}
	}
}
