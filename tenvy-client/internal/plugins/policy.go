package plugins

import (
	"crypto/ed25519"
	"encoding/hex"

	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

func BuiltInVerifyOptions() manifest.VerifyOptions {
	return manifest.VerifyOptions{
		SHA256AllowList: []string{
			"d8b8a0fb9c8f8e3a72d88e3f7a8c6d1f1fbb83c9f6c2ddacb12e3b45f1a8bbef",
			"97f3dfb04f2bb1d0d07b0eac07c6f6a7c6d820b4d19d3fbfdcf7ee52b0cc3947",
			"4fa1e33f99de3c58a4f0b6cbb9df450c3a7fd41b944fdc0bb70a0e0c3a4c299a",
		},
		Ed25519PublicKeys: map[string]ed25519.PublicKey{
			"release": decodeHexKey("ea9ceca1c7c7176859b235e095cbca9b5755746b741865cab5458d6f0e754cc2"),
		},
	}
}

func decodeHexKey(s string) ed25519.PublicKey {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("invalid hardcoded public key: " + err.Error())
	}
	return ed25519.PublicKey(b)
}
