package feedback

import (
	"crypto/rand"
	"math/big"
)

// refAlphabet is Crockford base32 without I, L, O and U — the characters that
// are misread or misheard. The ref is quoted back by the reporter ("R-7QK2"), so
// it has to survive being read aloud and written down.
const refAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// refDigits is the code length after the "R-" prefix: 32^4 ≈ 1.05M values, which
// at a household's volume makes a collision vanishingly rare. Generation
// therefore inserts and retries on the unique violation rather than pre-checking,
// which would be a race.
const refDigits = 4

// NewRef mints one candidate reference code.
func NewRef() (string, error) {
	b := make([]byte, refDigits)
	max := big.NewInt(int64(len(refAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = refAlphabet[n.Int64()]
	}
	return "R-" + string(b), nil
}

// ValidRef reports whether s has the shape the routes accept ("R-" + 4 Crockford
// base32 characters). Every route that takes a ref is either admin-gated or
// requires the site's widget key, so the code is a convenience, not a secret —
// but a malformed one should fail as a 404 rather than reach a query.
func ValidRef(s string) bool {
	if len(s) != 2+refDigits || s[0] != 'R' || s[1] != '-' {
		return false
	}
	for i := 2; i < len(s); i++ {
		found := false
		for j := 0; j < len(refAlphabet); j++ {
			if s[i] == refAlphabet[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
