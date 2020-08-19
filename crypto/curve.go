package crypto

import (
	"crypto/elliptic"
	"io"
	"math/big"
	"sync"
)

type BitCurve struct {
	P       *big.Int
	N       *big.Int
	B       *big.Int
	Gx, Gy  *big.Int
	BitSize int
}

func (BitCurve *BitCurve) Params() *elliptic.CurveParams {
	return &elliptic.CurveParams{
		P:       BitCurve.P,
		N:       BitCurve.N,
		B:       BitCurve.B,
		Gx:      BitCurve.Gx,
		Gy:      BitCurve.Gy,
		BitSize: BitCurve.BitSize,
	}
}

func (BitCurve *BitCurve) IsOnCurve(x, y *big.Int) bool {

	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, BitCurve.P)

	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)

	x3.Add(x3, BitCurve.B)
	x3.Mod(x3, BitCurve.P)

	return x3.Cmp(y2) == 0
}

func (BitCurve *BitCurve) affineFromJacobian(x, y, z *big.Int) (xOut, yOut *big.Int) {
	zinv := new(big.Int).ModInverse(z, BitCurve.P)
	zinvsq := new(big.Int).Mul(zinv, zinv)

	xOut = new(big.Int).Mul(x, zinvsq)
	xOut.Mod(xOut, BitCurve.P)
	zinvsq.Mul(zinvsq, zinv)
	yOut = new(big.Int).Mul(y, zinvsq)
	yOut.Mod(yOut, BitCurve.P)
	return
}

func (BitCurve *BitCurve) Add(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	z := new(big.Int).SetInt64(1)
	return BitCurve.affineFromJacobian(BitCurve.addJacobian(x1, y1, z, x2, y2, z))
}

func (BitCurve *BitCurve) addJacobian(x1, y1, z1, x2, y2, z2 *big.Int) (*big.Int, *big.Int, *big.Int) {

	z1z1 := new(big.Int).Mul(z1, z1)
	z1z1.Mod(z1z1, BitCurve.P)
	z2z2 := new(big.Int).Mul(z2, z2)
	z2z2.Mod(z2z2, BitCurve.P)

	u1 := new(big.Int).Mul(x1, z2z2)
	u1.Mod(u1, BitCurve.P)
	u2 := new(big.Int).Mul(x2, z1z1)
	u2.Mod(u2, BitCurve.P)
	h := new(big.Int).Sub(u2, u1)
	if h.Sign() == -1 {
		h.Add(h, BitCurve.P)
	}
	i := new(big.Int).Lsh(h, 1)
	i.Mul(i, i)
	j := new(big.Int).Mul(h, i)

	s1 := new(big.Int).Mul(y1, z2)
	s1.Mul(s1, z2z2)
	s1.Mod(s1, BitCurve.P)
	s2 := new(big.Int).Mul(y2, z1)
	s2.Mul(s2, z1z1)
	s2.Mod(s2, BitCurve.P)
	r := new(big.Int).Sub(s2, s1)
	if r.Sign() == -1 {
		r.Add(r, BitCurve.P)
	}
	r.Lsh(r, 1)
	v := new(big.Int).Mul(u1, i)

	x3 := new(big.Int).Set(r)
	x3.Mul(x3, x3)
	x3.Sub(x3, j)
	x3.Sub(x3, v)
	x3.Sub(x3, v)
	x3.Mod(x3, BitCurve.P)

	y3 := new(big.Int).Set(r)
	v.Sub(v, x3)
	y3.Mul(y3, v)
	s1.Mul(s1, j)
	s1.Lsh(s1, 1)
	y3.Sub(y3, s1)
	y3.Mod(y3, BitCurve.P)

	z3 := new(big.Int).Add(z1, z2)
	z3.Mul(z3, z3)
	z3.Sub(z3, z1z1)
	if z3.Sign() == -1 {
		z3.Add(z3, BitCurve.P)
	}
	z3.Sub(z3, z2z2)
	if z3.Sign() == -1 {
		z3.Add(z3, BitCurve.P)
	}
	z3.Mul(z3, h)
	z3.Mod(z3, BitCurve.P)

	return x3, y3, z3
}

func (BitCurve *BitCurve) Double(x1, y1 *big.Int) (*big.Int, *big.Int) {
	z1 := new(big.Int).SetInt64(1)
	return BitCurve.affineFromJacobian(BitCurve.doubleJacobian(x1, y1, z1))
}

func (BitCurve *BitCurve) doubleJacobian(x, y, z *big.Int) (*big.Int, *big.Int, *big.Int) {

	a := new(big.Int).Mul(x, x)
	b := new(big.Int).Mul(y, y)
	c := new(big.Int).Mul(b, b)

	d := new(big.Int).Add(x, b)
	d.Mul(d, d)
	d.Sub(d, a)
	d.Sub(d, c)
	d.Mul(d, big.NewInt(2))

	e := new(big.Int).Mul(big.NewInt(3), a)
	f := new(big.Int).Mul(e, e)

	x3 := new(big.Int).Mul(big.NewInt(2), d)
	x3.Sub(f, x3)
	x3.Mod(x3, BitCurve.P)

	y3 := new(big.Int).Sub(d, x3)
	y3.Mul(e, y3)
	y3.Sub(y3, new(big.Int).Mul(big.NewInt(8), c))
	y3.Mod(y3, BitCurve.P)

	z3 := new(big.Int).Mul(y, z)
	z3.Mul(big.NewInt(2), z3)
	z3.Mod(z3, BitCurve.P)

	return x3, y3, z3
}

func (BitCurve *BitCurve) ScalarMult(Bx, By *big.Int, k []byte) (*big.Int, *big.Int) {

	Bz := new(big.Int).SetInt64(1)
	x := Bx
	y := By
	z := Bz

	seenFirstTrue := false
	for _, byte := range k {
		for bitNum := 0; bitNum < 8; bitNum++ {
			if seenFirstTrue {
				x, y, z = BitCurve.doubleJacobian(x, y, z)
			}
			if byte&0x80 == 0x80 {
				if !seenFirstTrue {
					seenFirstTrue = true
				} else {
					x, y, z = BitCurve.addJacobian(Bx, By, Bz, x, y, z)
				}
			}
			byte <<= 1
		}
	}

	if !seenFirstTrue {
		return nil, nil
	}

	return BitCurve.affineFromJacobian(x, y, z)
}

func (BitCurve *BitCurve) ScalarBaseMult(k []byte) (*big.Int, *big.Int) {
	return BitCurve.ScalarMult(BitCurve.Gx, BitCurve.Gy, k)
}

var mask = []byte{0xff, 0x1, 0x3, 0x7, 0xf, 0x1f, 0x3f, 0x7f}

func (BitCurve *BitCurve) GenerateKey(rand io.Reader) (priv []byte, x, y *big.Int, err error) {
	byteLen := (BitCurve.BitSize + 7) >> 3
	priv = make([]byte, byteLen)

	for x == nil {
		_, err = io.ReadFull(rand, priv)
		if err != nil {
			return
		}

		priv[0] &= mask[BitCurve.BitSize%8]

		priv[1] ^= 0x42
		x, y = BitCurve.ScalarBaseMult(priv)
	}
	return
}

func (BitCurve *BitCurve) Marshal(x, y *big.Int) []byte {
	byteLen := (BitCurve.BitSize + 7) >> 3

	ret := make([]byte, 1+2*byteLen)
	ret[0] = 4

	xBytes := x.Bytes()
	copy(ret[1+byteLen-len(xBytes):], xBytes)
	yBytes := y.Bytes()
	copy(ret[1+2*byteLen-len(yBytes):], yBytes)
	return ret
}

func (BitCurve *BitCurve) Unmarshal(data []byte) (x, y *big.Int) {
	byteLen := (BitCurve.BitSize + 7) >> 3
	if len(data) != 1+2*byteLen {
		return
	}
	if data[0] != 4 {
		return
	}
	x = new(big.Int).SetBytes(data[1 : 1+byteLen])
	y = new(big.Int).SetBytes(data[1+byteLen:])
	return
}

var initonce sync.Once
var ecp160k1 *BitCurve
var ecp192k1 *BitCurve
var ecp224k1 *BitCurve
var ecp256k1 *BitCurve

func initAll() {
	initS160()
	initS192()
	initS224()
	initS256()
}

func initS160() {

	ecp160k1 = new(BitCurve)
	ecp160k1.P, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFAC73", 16)
	ecp160k1.N, _ = new(big.Int).SetString("0100000000000000000001B8FA16DFAB9ACA16B6B3", 16)
	ecp160k1.B, _ = new(big.Int).SetString("0000000000000000000000000000000000000007", 16)
	ecp160k1.Gx, _ = new(big.Int).SetString("3B4C382CE37AA192A4019E763036F4F5DD4D7EBB", 16)
	ecp160k1.Gy, _ = new(big.Int).SetString("938CF935318FDCED6BC28286531733C3F03C4FEE", 16)
	ecp160k1.BitSize = 160
}

func initS192() {

	ecp192k1 = new(BitCurve)
	ecp192k1.P, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFEE37", 16)
	ecp192k1.N, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFE26F2FC170F69466A74DEFD8D", 16)
	ecp192k1.B, _ = new(big.Int).SetString("000000000000000000000000000000000000000000000003", 16)
	ecp192k1.Gx, _ = new(big.Int).SetString("DB4FF10EC057E9AE26B07D0280B7F4341DA5D1B1EAE06C7D", 16)
	ecp192k1.Gy, _ = new(big.Int).SetString("9B2F2F6D9C5628A7844163D015BE86344082AA88D95E2F9D", 16)
	ecp192k1.BitSize = 192
}

func initS224() {

	ecp224k1 = new(BitCurve)
	ecp224k1.P, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFE56D", 16)
	ecp224k1.N, _ = new(big.Int).SetString("010000000000000000000000000001DCE8D2EC6184CAF0A971769FB1F7", 16)
	ecp224k1.B, _ = new(big.Int).SetString("00000000000000000000000000000000000000000000000000000005", 16)
	ecp224k1.Gx, _ = new(big.Int).SetString("A1455B334DF099DF30FC28A169A467E9E47075A90F7E650EB6B7A45C", 16)
	ecp224k1.Gy, _ = new(big.Int).SetString("7E089FED7FBA344282CAFBD6F7E319F7C0B0BD59E2CA4BDB556D61A5", 16)
	ecp224k1.BitSize = 224
}

func initS256() {

	ecp256k1 = new(BitCurve)
	ecp256k1.P, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F", 16)
	ecp256k1.N, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	ecp256k1.B, _ = new(big.Int).SetString("0000000000000000000000000000000000000000000000000000000000000007", 16)
	ecp256k1.Gx, _ = new(big.Int).SetString("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798", 16)
	ecp256k1.Gy, _ = new(big.Int).SetString("483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8", 16)
	ecp256k1.BitSize = 256
}

func S160() *BitCurve {
	initonce.Do(initAll)
	return ecp160k1
}

func S192() *BitCurve {
	initonce.Do(initAll)
	return ecp192k1
}

func S224() *BitCurve {
	initonce.Do(initAll)
	return ecp224k1
}

func S256() *BitCurve {
	initonce.Do(initAll)
	return ecp256k1
}
