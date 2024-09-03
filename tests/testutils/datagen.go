package testutils

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"time"

	bbndatagen "github.com/babylonlabs-io/babylon/testutil/datagen"
	"github.com/babylonlabs-io/staking-api-service/internal/config"
	"github.com/babylonlabs-io/staking-api-service/internal/types"
	"github.com/babylonlabs-io/staking-queue-client/client"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type TestActiveEventGeneratorOpts struct {
	NumOfEvents        int
	FinalityProviders  []string
	Stakers            []string
	EnforceNotOverflow bool
	BeforeTimestamp    int64
	AfterTimestamp     int64
}

func LoadTestConfig() *config.Config {
	cfg, err := config.New("../config/config-test.yml")
	if err != nil {
		log.Fatalf("Failed to load test config: %v", err)
	}
	return cfg
}

func RandomPk() (string, error) {
	fpPirvKey, err := btcec.NewPrivateKey()
	if err != nil {
		return "", err
	}
	fpPk := fpPirvKey.PubKey()
	return hex.EncodeToString(schnorr.SerializePubKey(fpPk)), nil
}

func GeneratePks(numOfKeys int) []string {
	var pks []string
	for i := 0; i < numOfKeys; i++ {
		k, err := RandomPk()
		if err != nil {
			log.Fatalf("Failed to generate random pk: %v", err)
		}
		pks = append(pks, k)
	}
	return pks
}

// RandomPostiveFloat64 generates a random float64 value greater than 0.
func RandomPostiveFloat64(r *rand.Rand) float64 {
	for {
		f := r.Float64() // Generate a random float64
		if f > 0 {
			return f
		}
		// If f is 0 (extremely rare), regenerate
	}
}

// RandomPositiveInt generates a random positive integer from 1 to max.
func RandomPositiveInt(r *rand.Rand, max int) int {
	// Generate a random number from 1 to max (inclusive)
	return r.Intn(max) + 1
}

// RandomString generates a random alphanumeric string of length n.
func RandomString(r *rand.Rand, n int) string {
	result := make([]byte, n)
	letterLen := len(letters)
	for i := range result {
		num := r.Int() % letterLen
		result[i] = letters[num]
	}
	return string(result)
}

// RandomAmount generates a random BTC amount from 0.1 to 10000
// the returned value is in satoshis
func RandomAmount(r *rand.Rand) int64 {
	// Generate a random value range from 0.1 to 10000 BTC
	randomBTC := r.Float64()*(9999.9-0.1) + 0.1
	// convert to satoshi
	return int64(randomBTC*1e8) + 1
}

// GenerateRandomTx generates a random transaction with random values for each field.
func GenerateRandomTx(
	r *rand.Rand,
	options *struct{ DisableRbf bool },
) (*wire.MsgTx, string, error) {
	sequence := r.Uint32()
	if options != nil && options.DisableRbf {
		sequence = wire.MaxTxInSequenceNum
	}
	tx := &wire.MsgTx{
		Version: 1,
		TxIn: []*wire.TxIn{
			{
				PreviousOutPoint: wire.OutPoint{
					Hash:  chainhash.HashH(bbndatagen.GenRandomByteArray(r, 10)),
					Index: r.Uint32(),
				},
				SignatureScript: bbndatagen.GenRandomByteArray(r, 10),
				Sequence:        sequence,
			},
		},
		TxOut: []*wire.TxOut{
			{
				Value:    int64(r.Int31()),
				PkScript: bbndatagen.GenRandomByteArray(r, 80),
			},
		},
		LockTime: 0,
	}
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, "", err
	}
	txHex := hex.EncodeToString(buf.Bytes())

	return tx, txHex, nil
}

// GenerateRandomTxWithOutput generates a random transaction with random values
// for each field.
func RandomBytes(r *rand.Rand, n uint64) ([]byte, string) {
	randomBytes := bbndatagen.GenRandomByteArray(r, n)
	return randomBytes, hex.EncodeToString(randomBytes)
}

// GenerateRandomTimestamp generates a random timestamp before the specified timestamp.
// If beforeTimestamp is 0, then the current time is used.
func GenerateRandomTimestamp(afterTimestamp, beforeTimestamp int64) int64 {
	timeNow := time.Now().Unix()
	if beforeTimestamp == 0 && afterTimestamp == 0 {
		return timeNow
	}
	if beforeTimestamp == 0 {
		return afterTimestamp + rand.Int63n(timeNow-afterTimestamp)
	} else if afterTimestamp == 0 {
		// Generate a reasonable timestamp between 1 second to 6 months in the past
		sixMonthsInSeconds := int64(6 * 30 * 24 * 60 * 60)
		return beforeTimestamp - rand.Int63n(sixMonthsInSeconds)
	}
	return afterTimestamp + rand.Int63n(beforeTimestamp-afterTimestamp)
}

// GenerateRandomFinalityProviderDetail generates a random number of finality providers
func GenerateRandomFinalityProviderDetail(r *rand.Rand, numOfFps uint64) []types.FinalityProviderDetails {
	var finalityProviders []types.FinalityProviderDetails

	for i := uint64(0); i < numOfFps; i++ {
		fpPkInHex, err := RandomPk()
		if err != nil {
			log.Fatalf("failed to generate random public key: %v", err)
		}

		randomStr := RandomString(r, 10)
		finalityProviders = append(finalityProviders, types.FinalityProviderDetails{
			Description: types.FinalityProviderDescription{
				Moniker:         "Moniker" + randomStr,
				Identity:        "Identity" + randomStr,
				Website:         "Website" + randomStr,
				SecurityContact: "SecurityContact" + randomStr,
				Details:         "Details" + randomStr,
			},
			Commission: fmt.Sprintf("%f", RandomPostiveFloat64(r)),
			BtcPk:      fpPkInHex,
		})
	}
	return finalityProviders
}

// GenerateRandomActiveStakingEvents generates a random number of active staking events
// with random values for each field.
// default to max 11 events, 11 finality providers, and 11 stakers
func GenerateRandomActiveStakingEvents(
	r *rand.Rand, opts *TestActiveEventGeneratorOpts,
) []*client.ActiveStakingEvent {
	var activeStakingEvents []*client.ActiveStakingEvent
	genOpts := &TestActiveEventGeneratorOpts{
		NumOfEvents:       11,
		FinalityProviders: GeneratePks(11),
		Stakers:           GeneratePks(11),
	}

	if opts != nil {
		if opts.NumOfEvents > 0 {
			genOpts.NumOfEvents = opts.NumOfEvents
		}
		if len(opts.FinalityProviders) > 0 {
			genOpts.FinalityProviders = opts.FinalityProviders
		}
		if len(opts.Stakers) > 0 {
			genOpts.Stakers = opts.Stakers
		}
	}

	fpPks := genOpts.FinalityProviders
	stakerPks := genOpts.Stakers

	for i := 0; i < genOpts.NumOfEvents; i++ {
		randomFpPk := fpPks[rand.Intn(len(fpPks))]
		randomStakerPk := stakerPks[rand.Intn(len(stakerPks))]
		tx, hex, err := GenerateRandomTx(r, nil)
		if err != nil {
			log.Fatalf("failed to generate random tx: %v", err)
		}
		var isOverflow bool
		if opts.EnforceNotOverflow {
			isOverflow = false
		} else {
			isOverflow = rand.Int()%2 == 0
		}
		activeStakingEvent := &client.ActiveStakingEvent{
			EventType:             client.ActiveStakingEventType,
			StakingTxHashHex:      tx.TxHash().String(),
			StakerPkHex:           randomStakerPk,
			FinalityProviderPkHex: randomFpPk,
			StakingValue:          uint64(RandomAmount(r)),
			StakingStartHeight:    uint64(RandomPositiveInt(r, 100000)),
			StakingStartTimestamp: GenerateRandomTimestamp(
				opts.AfterTimestamp, opts.BeforeTimestamp,
			),
			StakingTimeLock:    uint64(rand.Intn(100)),
			StakingOutputIndex: uint64(rand.Intn(100)),
			StakingTxHex:       hex,
			IsOverflow:         isOverflow,
		}
		activeStakingEvents = append(activeStakingEvents, activeStakingEvent)
	}
	return activeStakingEvents
}