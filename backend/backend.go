// Steve Phillips / elimisteve
// 2015.03.01

package backend

import (
	"errors"
	"fmt"
	"log"

	"github.com/cryptag/cryptag"
	"github.com/cryptag/cryptag/types"
	"github.com/elimisteve/fun"
)

var (
	RANDOM_TAG_ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789"
	RANDOM_TAG_LENGTH   = 9

	ErrBackendExists = errors.New("Backend already exists")
)

// Backend is an interface that represents a type of storage location
// for data, such as a filesystem or remote API.
type Backend interface {
	Name() string
	Key() *[32]byte

	AllTagPairs(oldPairs types.TagPairs) (types.TagPairs, error)
	TagPairsFromRandomTags(randtags cryptag.RandomTags) (types.TagPairs, error)
	SaveTagPair(pair *types.TagPair) error

	ListRows(randtags cryptag.RandomTags) (types.Rows, error)
	RowsFromRandomTags(randtags cryptag.RandomTags) (types.Rows, error)
	SaveRow(row *types.Row) error
	DeleteRows(randtags cryptag.RandomTags) error

	ToConfig() (*Config, error)
}

// CreateTagsFromPlain concurrently creates new TagPairs for each
// plaintag that doesn't already have a corresponding PlainTag in
// pairs.  (Be sure that pairs contains the latest TagPairs contained
// in backend.)
func CreateTagsFromPlain(bk Backend, plaintags []string, pairs types.TagPairs) (newPairs types.TagPairs, err error) {
	// Find out which members of plaintags don't have an existing,
	// corresponding TagPair

	existingPlain := pairs.AllPlain()

	// Concurrent Tag creation ftw
	var chs []chan *types.TagPair

	// TODO: Put the following in a `CreateTags` function

	for _, plain := range plaintags {
		if !fun.SliceContains(existingPlain, plain) {
			// Preserve tag ordering despite concurrent creation
			ch := make(chan *types.TagPair)
			chs = append(chs, ch)

			go func(plain string, ch chan *types.TagPair) {
				pair, err := CreateTag(bk, plain)
				if err != nil {
					log.Printf("Error calling CreateTag(%q): %v\n", plain, err)
					ch <- nil
					return
				}
				if types.Debug {
					log.Printf("Created TagPair{plain: %q, Random: %q}\n",
						pair.Plain(), pair.Random)
				}
				ch <- pair
				return
			}(plain, ch)
		}
	}

	// Append successfully-created *TagPair values to `chs`
	//
	// TODO: Consider timing out in case CreateTag() never returns
	for i := 0; i < len(chs); i++ {
		if p := <-chs[i]; p != nil {
			newPairs = append(newPairs, p)
		}
	}

	return newPairs, nil
}

// NewTagPair creates a (cryptographically secure pseudorandom)
// RandomTag that corresponds to the given PlainTag, generates a new
// nonce, encrypts the PlainTag, then creates and returns the newly
// allocated TagPair.
func NewTagPair(key *[32]byte, plaintag string) (*types.TagPair, error) {
	rand := fun.RandomString(RANDOM_TAG_ALPHABET, RANDOM_TAG_LENGTH)

	nonce, err := cryptag.RandomNonce()
	if err != nil {
		return nil, err
	}

	plainEnc, err := cryptag.Encrypt([]byte(plaintag), nonce, key)
	if err != nil {
		return nil, err
	}

	pair := types.NewTagPair(plainEnc, rand, nonce, plaintag)

	return pair, nil
}

// CreateTag uses NewTagPair to create a new TagPair, then saves said
// TagPair in backend.
func CreateTag(bk Backend, plaintag string) (*types.TagPair, error) {
	pair, err := NewTagPair(bk.Key(), plaintag)
	if err != nil {
		return nil, err
	}

	err = bk.SaveTagPair(pair)
	if err != nil {
		return nil, fmt.Errorf("Error saving tag pair to backend %v: %v",
			bk.Name(), err)
	}

	return pair, nil
}

// PopulateRowBeforeSave creates a new TagPair for each plaintag
// unique to row, sets row.RandomTags, and sets row.Encrypted.  row is
// now ready to be saved to a Backend.
func PopulateRowBeforeSave(bk Backend, row *types.Row, pairs types.TagPairs) (newPairs types.TagPairs, err error) {
	// For each element of row.plainTags that doesn't match an
	// existing tag, call CreateTag().  Encrypt row.decrypted and
	// store it in row.Encrypted.  POST to server.

	// TODO: Call this in parallel with encryption below
	newPairs, err = CreateTagsFromPlain(bk, row.PlainTags(), pairs)
	if err != nil {
		return newPairs, fmt.Errorf("Error from CreateNewTagsFromPlain: %v", err)
	}

	allTagPairs := append(pairs, newPairs...)

	var randtags []string

	// Set row.RandomTags

	for _, plain := range row.PlainTags() {
		for i, pair := range allTagPairs {
			if plain == pair.Plain() {
				randtags = append(randtags, pair.Random)
				break
			}
			if i == len(allTagPairs)-1 {
				return newPairs, fmt.Errorf(
					"No corresponding TagPair found for plain tag `%s`", plain)
			}
		}
	}
	row.RandomTags = randtags

	// Set row.Encrypted

	encData, err := cryptag.Encrypt(row.Decrypted(), row.Nonce, bk.Key())
	if err != nil {
		return newPairs, fmt.Errorf("Error encrypting data: %v", err)
	}
	row.Encrypted = encData

	return newPairs, nil
}
