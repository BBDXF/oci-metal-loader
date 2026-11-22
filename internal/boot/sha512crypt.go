package boot

import (
	"crypto/rand"
	"crypto/sha512"
	"fmt"
	"strconv"
	"strings"
)

const cryptB64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Sha512Crypt implements glibc's "$6$" crypt (Drepper's SHA-crypt). setting is
// "$6$[rounds=N$]salt[$...]"; the result is the full "$6$..." string.
func Sha512Crypt(pw, setting string) (string, error) {
	if !strings.HasPrefix(setting, "$6$") {
		return "", fmt.Errorf("not a $6$ setting")
	}
	rest := setting[3:]
	rounds, custom := 5000, false
	if strings.HasPrefix(rest, "rounds=") {
		end := strings.IndexByte(rest, '$')
		if end < 0 {
			return "", fmt.Errorf("bad rounds field")
		}
		n, err := strconv.Atoi(rest[7:end])
		if err != nil {
			return "", fmt.Errorf("bad rounds: %v", err)
		}
		if n < 1000 {
			n = 1000
		}
		if n > 999999999 {
			n = 999999999
		}
		rounds, custom, rest = n, true, rest[end+1:]
	}
	if i := strings.IndexByte(rest, '$'); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) > 16 {
		rest = rest[:16]
	}
	p, s := []byte(pw), []byte(rest)
	sum := func(parts ...[]byte) []byte {
		h := sha512.New()
		for _, x := range parts {
			h.Write(x)
		}
		return h.Sum(nil)
	}
	b := sum(p, s, p)
	ah := sha512.New()
	ah.Write(p)
	ah.Write(s)
	for n := len(p); n > 0; n -= 64 {
		if n > 64 {
			ah.Write(b)
		} else {
			ah.Write(b[:n])
		}
	}
	for n := len(p); n > 0; n >>= 1 {
		if n&1 == 1 {
			ah.Write(b)
		} else {
			ah.Write(p)
		}
	}
	a := ah.Sum(nil)
	dph := sha512.New()
	for i := 0; i < len(p); i++ {
		dph.Write(p)
	}
	rep := func(src []byte, n int) []byte {
		out := make([]byte, 0, n)
		for len(out) < n {
			k := n - len(out)
			if k > len(src) {
				k = len(src)
			}
			out = append(out, src[:k]...)
		}
		return out
	}
	pp := rep(dph.Sum(nil), len(p))
	dsh := sha512.New()
	for i := 0; i < 16+int(a[0]); i++ {
		dsh.Write(s)
	}
	ss := rep(dsh.Sum(nil), len(s))
	for i := 0; i < rounds; i++ {
		h := sha512.New()
		if i&1 == 1 {
			h.Write(pp)
		} else {
			h.Write(a)
		}
		if i%3 != 0 {
			h.Write(ss)
		}
		if i%7 != 0 {
			h.Write(pp)
		}
		if i&1 == 1 {
			h.Write(a)
		} else {
			h.Write(pp)
		}
		a = h.Sum(nil)
	}
	var out strings.Builder
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for i := 0; i < n; i++ {
			out.WriteByte(cryptB64[w&0x3f])
			w >>= 6
		}
	}
	idx := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7},
		{50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57},
		{37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
	for _, t := range idx {
		enc(a[t[0]], a[t[1]], a[t[2]], 4)
	}
	enc(0, 0, a[63], 2)
	res := "$6$"
	if custom {
		res += "rounds=" + strconv.Itoa(rounds) + "$"
	}
	return res + rest + "$" + out.String(), nil
}

// HashPassword returns a fresh "$6$<random salt>$..." hash of pw.
func HashPassword(pw string) (string, error) {
	var r [16]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = cryptB64[int(r[i])%64]
	}
	return Sha512Crypt(pw, "$6$"+string(salt))
}
