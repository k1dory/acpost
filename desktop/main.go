//go:build darwin

// Command acpost-desktop is the macOS app of acpost: conversations with agents (on this node, on
// other nodes through the hub, in cloud sandboxes), what they do as they do it, approvals,
// changes, pull requests and CI, a terminal and usage. It is a client of the local node, as the
// command line is.
package main

import (
	"embed"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	node := &Node{}
	services := []application.Service{application.NewService(node)}
	// Notifications of the system need the bundle's identifier: from acpost.app only.
	if exe, err := os.Executable(); err == nil && strings.Contains(exe, ".app/Contents/MacOS/") {
		node.notes = notifications.New()
		services = append(services, application.NewService(node.notes))
	}
	app := application.New(application.Options{
		Name:        "acpost",
		Description: "Conversations with agents",
		Services:    services,
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	node.app = app
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "acpost",
		Width:     1440,
		Height:    900,
		MinWidth:  980,
		MinHeight: 620,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropNormal,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(246, 246, 243),
		URL:              "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
