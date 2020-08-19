package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"os"

	"encoding/hex"
	"errors"

	"code.google.com/p/go-uuid/uuid"
	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/crypto/ecies"
	"github.com/bhu/bhu-chain/crypto/secp256k1"
	"github.com/bhu/bhu-chain/crypto/sha3"
	"golang.org/x/crypto/ripemd160"
)

var secp256k1n *big.Int

func init() {

	ecies.AddParamsForCurve(S256(), ecies.ECIES_AES128_SHA256)
	secp256k1n = common.String2Big("0xfffffffffffffffffffffffffffffffebaaedce6af40ca42378bfd25e8cd0364141")
}

func Sha3(data ...[]byte) []byte {
	d := sha3.NewKeccak256()
	for _, b := range data {
		d.Write(b)
	}
	return d.Sum(nil)
}

func Sha3Hash(data ...[]byte) (h common.Hash) {
	d := sha3.NewKeccak256()
	for _, b := range data {
		d.Write(b)
	}
	d.Sum(h[:0])
	return h
}

func CreateAddress(b common.Address, nonce uint64) common.Address {
	data, _ := EncodeToBytes([]interface{}{b, nonce})
	return common.BytesToAddress(Sha3(data)[12:])

}

func Sha256(data []byte) []byte {
	hash := sha256.Sum256(data)

	return hash[:]
}

func Ripemd160(data []byte) []byte {
	ripemd := ripemd160.New()
	ripemd.Write(data)

	return ripemd.Sum(nil)
}

func Ecrecover(hash, sig []byte) ([]byte, error) {
	return secp256k1.RecoverPubkey(hash, sig)
}

func ToECDSA(prv []byte) *ecdsa.PrivateKey {
	if len(prv) == 0 {
		return nil
	}

	priv := new(ecdsa.PrivateKey)
	priv.PublicKey.Curve = S256()
	priv.D = common.BigD(prv)
	priv.PublicKey.X, priv.PublicKey.Y = S256().ScalarBaseMult(prv)
	return priv
}

func FromECDSA(prv *ecdsa.PrivateKey) []byte {
	if prv == nil {
		return nil
	}
	return prv.D.Bytes()
}

func ToECDSAPub(pub []byte) *ecdsa.PublicKey {
	if len(pub) == 0 {
		return nil
	}
	x, y := elliptic.Unmarshal(S256(), pub)
	return &ecdsa.PublicKey{S256(), x, y}
}

func FromECDSAPub(pub *ecdsa.PublicKey) []byte {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil
	}
	return elliptic.Marshal(S256(), pub.X, pub.Y)
}

func HexToECDSA(hexkey string) (*ecdsa.PrivateKey, error) {
	b, err := hex.DecodeString(hexkey)
	if err != nil {
		return nil, errors.New("invalid hex string")
	}
	if len(b) != 32 {
		return nil, errors.New("invalid length, need 256 bits")
	}
	return ToECDSA(b), nil
}

func LoadECDSA(file string) (*ecdsa.PrivateKey, error) {
	buf := make([]byte, 64)
	fd, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	if _, err := io.ReadFull(fd, buf); err != nil {
		return nil, err
	}

	key, err := hex.DecodeString(string(buf))
	if err != nil {
		return nil, err
	}

	return ToECDSA(key), nil
}

func SaveECDSA(file string, key *ecdsa.PrivateKey) error {
	k := hex.EncodeToString(FromECDSA(key))
	return ioutil.WriteFile(file, []byte(k), 0600)
}

func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(S256(), rand.Reader)
}

func ValidateSignatureValues(v byte, r, s *big.Int) bool {
	vint := uint32(v)
	if r.Cmp(common.Big0) == 0 || s.Cmp(common.Big0) == 0 {
		return false
	}
	if r.Cmp(secp256k1n) < 0 && s.Cmp(secp256k1n) < 0 && (vint == 27 || vint == 28) {
		return true
	} else {
		return false
	}
}

func SigToPub(hash, sig []byte) (*ecdsa.PublicKey, error) {
	s, err := Ecrecover(hash, sig)
	if err != nil {
		return nil, err
	}

	x, y := elliptic.Unmarshal(S256(), s)
	return &ecdsa.PublicKey{S256(), x, y}, nil
}

func Sign(hash []byte, prv *ecdsa.PrivateKey) (sig []byte, err error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("hash is required to be exactly 32 bytes (%d)", len(hash))
	}

	sig, err = secp256k1.Sign(hash, common.LeftPadBytes(prv.D.Bytes(), prv.Params().BitSize/8))
	return
}

func Encrypt(pub *ecdsa.PublicKey, message []byte) ([]byte, error) {
	return ecies.Encrypt(rand.Reader, ecies.ImportECDSAPublic(pub), message, nil, nil)
}

func Decrypt(prv *ecdsa.PrivateKey, ct []byte) ([]byte, error) {
	key := ecies.ImportECDSA(prv)
	return key.Decrypt(rand.Reader, ct, nil, nil)
}

func ImportBlockTestKey(privKeyBytes []byte) error {
	ks := NewKeyStorePassphrase(common.DefaultDataDir() + "/keystore")
	ecKey := ToECDSA(privKeyBytes)
	key := &Key{
		Id:         uuid.NewRandom(),
		Address:    PubkeyToAddress(ecKey.PublicKey),
		PrivateKey: ecKey,
	}
	err := ks.StoreKey(key, "")
	return err
}

func ImportPreSaleKey(keyStore KeyStore, keyJSON []byte, password string) (*Key, error) {
	key, err := decryptPreSaleKey(keyJSON, password)
	if err != nil {
		return nil, err
	}
	key.Id = uuid.NewRandom()
	err = keyStore.StoreKey(key, password)
	return key, err
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, err
}

func aesCBCDecrypt(key, cipherText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	decrypter := cipher.NewCBCDecrypter(aesBlock, iv)
	paddedPlaintext := make([]byte, len(cipherText))
	decrypter.CryptBlocks(paddedPlaintext, cipherText)
	plaintext := PKCS7Unpad(paddedPlaintext)
	if plaintext == nil {
		err = errors.New("Decryption failed: PKCS7Unpad failed after AES decryption")
	}
	return plaintext, err
}

func PKCS7Pad(in []byte) []byte {
	padding := 16 - (len(in) % 16)
	if padding == 0 {
		padding = 16
	}
	for i := 0; i < padding; i++ {
		in = append(in, byte(padding))
	}
	return in
}

func PKCS7Unpad(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	padding := in[len(in)-1]
	if int(padding) > len(in) || padding > aes.BlockSize {
		return nil
	} else if padding == 0 {
		return nil
	}

	for i := len(in) - 1; i > len(in)-int(padding)-1; i-- {
		if in[i] != padding {
			return nil
		}
	}
	return in[:len(in)-int(padding)]
}

func PubkeyToAddress(p ecdsa.PublicKey) common.Address {
	pubBytes := FromECDSAPub(&p)
	return common.BytesToAddress(Sha3(pubBytes[1:])[12:])
}
