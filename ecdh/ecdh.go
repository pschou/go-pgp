// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ecdh implements ECDH encryption, suitable for OpenPGP,
// as specified in RFC 6637, section 8.
package ecdh

import (
	"bytes"
	"crypto/elliptic"
	"errors"
	"io"
	"math/big"

	"github.com/pschou/go_openpgp/aes/keywrap"
	"github.com/pschou/go_openpgp/internal/algorithm"
)

type KDF struct {
	Hash   algorithm.Hash
	Cipher algorithm.Cipher
}

type PublicKey struct {
	elliptic.Curve

	X, Y *big.Int
	KDF
}

type PrivateKey struct {
	PublicKey
	D []byte
}

func GenerateKey(c elliptic.Curve, kdf KDF, rand io.Reader) (priv *PrivateKey, err error) {
	priv = new(PrivateKey)
	priv.PublicKey.Curve = c
	priv.PublicKey.KDF = kdf
	priv.D, priv.PublicKey.X, priv.PublicKey.Y, err = elliptic.GenerateKey(c, rand)
	return
}

func Encrypt(random io.Reader, pub *PublicKey, msg, curveOID, fingerprint []byte) (vsG, c []byte, err error) {
	if len(msg) > 40 {
		return nil, nil, errors.New("ecdh: message too long")
	}

	// the sender MAY use 21, 13, and 5 bytes of padding for AES-128,
	// AES-192, and AES-256, respectively, to provide the same number of
	// octets, 40 total, as an input to the key wrapping method.
	padding := make([]byte, 40-len(msg))
	for i := range padding {
		padding[i] = byte(40 - len(msg))
	}
	m := append(msg, padding...)

	d, x, y, err := elliptic.GenerateKey(pub.Curve, random)
	if err != nil {
		return nil, nil, err
	}

	vsG = elliptic.Marshal(pub.Curve, x, y)
	zb, _ := pub.Curve.ScalarMult(pub.X, pub.Y, d)

	z, err := buildKey(pub, zb, curveOID, fingerprint)
	if err != nil {
		return nil, nil, err
	}

	if c, err = keywrap.Wrap(z, m); err != nil {
		return nil, nil, err
	}

	return vsG, c, nil

}

func Decrypt(priv *PrivateKey, vsG, m, curveOID, fingerprint []byte) (msg []byte, err error) {
	x, y := elliptic.Unmarshal(priv.Curve, vsG)
	zb, _ := priv.Curve.ScalarMult(x, y, priv.D)

	z, err := buildKey(&priv.PublicKey, zb, curveOID, fingerprint)
	if err != nil {
		return nil, err
	}

	c, err := keywrap.Unwrap(z, m)
	if err != nil {
		return nil, err
	}

	return c[:len(c)-int(c[len(c)-1])], nil
}

func buildKey(pub *PublicKey, zb *big.Int, curveOID, fingerprint []byte) ([]byte, error) {
	// Param = curve_OID_len || curve_OID || public_key_alg_ID || 03
	//         || 01 || KDF_hash_ID || KEK_alg_ID for AESKeyWrap
	//         || "Anonymous Sender    " || recipient_fingerprint;
	param := new(bytes.Buffer)
	if _, err := param.Write(curveOID); err != nil {
		return nil, err
	}
	algKDF := []byte{18, 3, 1, pub.KDF.Hash.Id(), pub.KDF.Cipher.Id()}
	if _, err := param.Write(algKDF); err != nil {
		return nil, err
	}
	if _, err := param.Write([]byte("Anonymous Sender    ")); err != nil {
		return nil, err
	}
	if _, err := param.Write(fingerprint); err != nil {
		return nil, err
	}
	if param.Len() != 54 && param.Len() != 51 {
		return nil, errors.New("ecdh: malformed KDF Param")
	}

	// MB = Hash ( 00 || 00 || 00 || 01 || ZB || Param );
	h := pub.KDF.Hash.New()
	if _, err := h.Write([]byte{0x0, 0x0, 0x0, 0x1}); err != nil {
		return nil, err
	}
	if _, err := h.Write(zb.Bytes()); err != nil {
		return nil, err
	}
	if _, err := h.Write(param.Bytes()); err != nil {
		return nil, err
	}
	mb := h.Sum(nil)

	return mb[:pub.KDF.Cipher.KeySize()], nil // return oBits leftmost bits of MB.

}
