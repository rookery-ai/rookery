package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// The envelope is a small authenticated header followed by AES-256-GCM frames.
//
// Framing rather than one-shot GCM buys three properties a single seal cannot:
// bounded memory regardless of vault size, detection of reordered frames (the
// frame index is authenticated), and detection of truncation (the final flag is
// authenticated, so a stream cut short by a failed upload cannot decrypt
// cleanly into a partial archive).
const (
	magic           = "SABACKUP"
	envelopeVersion = 1
	kdfArgon2id     = 1

	// Argon2id parameters — identical to internal/secrets, deliberately.
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32

	chunkSize = 1 << 20 // 1 MiB of plaintext per frame
	saltLen   = 16
	nonceLen  = 12
	headerLen = 8 + 1 + 1 + 4 + 4 + 1 + saltLen
)

var (
	// ErrBadPassphrase means the first frame failed to authenticate, which in
	// practice always means a wrong passphrase.
	ErrBadPassphrase = errors.New("backup: wrong passphrase")
	// ErrCorrupt means the stream is not a valid snapshot: bad magic, an
	// unknown version, a truncated stream, or a tampered frame.
	ErrCorrupt = errors.New("backup: snapshot is corrupt or truncated")
)

// buildHeader renders the envelope header. It is written in the clear and
// authenticated as AAD, so parameter tampering is detectable.
func buildHeader(salt []byte) []byte {
	h := make([]byte, 0, headerLen)
	h = append(h, magic...)
	h = append(h, envelopeVersion, kdfArgon2id)
	h = binary.BigEndian.AppendUint32(h, argonTime)
	h = binary.BigEndian.AppendUint32(h, argonMemory)
	h = append(h, argonThreads)
	h = append(h, salt...)
	return h
}

// frameAAD binds each frame to the header, its position, and whether it ends
// the stream.
func frameAAD(header []byte, index uint64, final bool) []byte {
	aad := make([]byte, 0, len(header)+9)
	aad = append(aad, header...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return aad
}

func newAEAD(passphrase string, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals src into dst. dst receives the header followed by one frame per
// chunkSize of plaintext; a zero-length input still produces one (empty) final
// frame so that every stream has an authenticated terminator.
func Encrypt(dst io.Writer, src io.Reader, passphrase string) error {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("backup: generate salt: %w", err)
	}
	header := buildHeader(salt)
	if _, err := dst.Write(header); err != nil {
		return fmt.Errorf("backup: write header: %w", err)
	}

	aead, err := newAEAD(passphrase, salt)
	if err != nil {
		return err
	}

	buf := make([]byte, chunkSize)
	var index uint64
	for {
		n, readErr := io.ReadFull(src, buf)
		final := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
		if readErr != nil && !final {
			return fmt.Errorf("backup: read plaintext: %w", readErr)
		}

		nonce := make([]byte, nonceLen)
		if _, err := rand.Read(nonce); err != nil {
			return fmt.Errorf("backup: generate nonce: %w", err)
		}
		sealed := aead.Seal(nil, nonce, buf[:n], frameAAD(header, index, final))

		if err := binary.Write(dst, binary.BigEndian, uint32(len(sealed))); err != nil {
			return fmt.Errorf("backup: write frame length: %w", err)
		}
		if _, err := dst.Write(nonce); err != nil {
			return fmt.Errorf("backup: write nonce: %w", err)
		}
		if _, err := dst.Write(sealed); err != nil {
			return fmt.Errorf("backup: write frame: %w", err)
		}

		if final {
			return nil
		}
		index++
	}
}

// Decrypt opens a stream produced by Encrypt and writes the plaintext to dst.
// It stops at the frame marked final; a stream that ends without one is
// truncated and reported as ErrCorrupt.
func Decrypt(dst io.Writer, src io.Reader, passphrase string) error {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(src, header); err != nil {
		return fmt.Errorf("%w: short header", ErrCorrupt)
	}
	if string(header[:8]) != magic {
		return fmt.Errorf("%w: not a rookery snapshot", ErrCorrupt)
	}
	if header[8] != envelopeVersion {
		return fmt.Errorf("%w: unsupported snapshot version %d", ErrCorrupt, header[8])
	}
	if header[9] != kdfArgon2id {
		return fmt.Errorf("%w: unsupported kdf %d", ErrCorrupt, header[9])
	}

	aead, err := newAEAD(passphrase, header[headerLen-saltLen:])
	if err != nil {
		return err
	}

	var index uint64
	for {
		var length uint32
		if err := binary.Read(src, binary.BigEndian, &length); err != nil {
			// Running out of frames without ever seeing the final flag is the
			// signature of a truncated upload.
			return fmt.Errorf("%w: stream ended without a final frame", ErrCorrupt)
		}
		if length < uint32(aead.Overhead()) || length > chunkSize+uint32(aead.Overhead()) {
			return fmt.Errorf("%w: implausible frame length %d", ErrCorrupt, length)
		}

		nonce := make([]byte, nonceLen)
		if _, err := io.ReadFull(src, nonce); err != nil {
			return fmt.Errorf("%w: short nonce", ErrCorrupt)
		}
		sealed := make([]byte, length)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return fmt.Errorf("%w: short frame", ErrCorrupt)
		}

		// Try the non-final interpretation first, then the final one. Only the
		// AAD differs, so exactly one can authenticate.
		plain, err := aead.Open(nil, nonce, sealed, frameAAD(header, index, false))
		final := false
		if err != nil {
			plain, err = aead.Open(nil, nonce, sealed, frameAAD(header, index, true))
			final = true
		}
		if err != nil {
			// A first frame that reads fully but fails to authenticate is
			// ambiguous: a wrong passphrase and a corrupted frame 0 are
			// indistinguishable, because the key derives from the passphrase and
			// nothing else is available to check it against. Wrong passphrase is
			// overwhelmingly the common case, so it is reported — at the cost
			// that a snapshot damaged in its first frame sends the owner hunting
			// for a credential problem that does not exist. Every other form of
			// damage (bad magic, short header, short frame, implausible length)
			// is reported as ErrCorrupt before reaching here.
			if index == 0 {
				return ErrBadPassphrase
			}
			return fmt.Errorf("%w: frame %d failed authentication", ErrCorrupt, index)
		}

		if _, err := dst.Write(plain); err != nil {
			return fmt.Errorf("backup: write plaintext: %w", err)
		}
		if final {
			return nil
		}
		index++
	}
}
