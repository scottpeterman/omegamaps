// internal/crawldial/dialer.go
//
// The crawl front ends' view of the dial layer.
//
// The dial primitives themselves live in internal/dial, which knows nothing
// about crawls — capture uses the same ones. What is left here is the crawl's
// adaptation of them: aliases so cmd/crawl and cmd/crawlui keep reading
// crawldial.BaseConfig, and the one genuinely crawl-shaped piece, which is
// turning a first-contact host key into a crawlrun event.
package crawldial

import (
	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/dial"
)

// BaseConfig is dial.BaseConfig. Aliased rather than wrapped so that setting a
// field the crawl does not know about is still possible without this file
// having to grow every time dial does.
type BaseConfig = dial.BaseConfig

// VaultDialer is dial.Vault.
type VaultDialer = dial.Vault

// StaticDialer uses one credential for every device.
func StaticDialer(base BaseConfig, user, password, keyPath string) crawler.DialFunc {
	return dial.Static(base, user, password, keyPath)
}

// HostKeyEmitter adapts dial's plain first-contact callback to a crawlrun
// event, so a TOFU acceptance lands in the decisions pane instead of scrolling
// past on stderr.
//
// This is the whole reason internal/dial does not import crawlrun: a package
// named "dial" that depends on a package named "crawlrun" is a dependency
// nobody would predict from the name, and capture would have inherited it.
func HostKeyEmitter(emit crawlrun.Emit) dial.NewHostKeyFunc {
	return func(host, keyType, fingerprint string) {
		emit.Send(crawlrun.Event{
			Kind:     crawlrun.KindHostKeyNew,
			Identity: host,
			Detail:   keyType + " " + fingerprint,
		})
	}
}
