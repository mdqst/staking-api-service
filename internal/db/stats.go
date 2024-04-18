package db

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/babylonchain/staking-api-service/internal/db/model"
	"github.com/babylonchain/staking-api-service/internal/types"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetOrCreateStatsLock fetches the lock status for each stats type for the given staking tx hash.
// If the document does not exist, it will create a new document with the default values
// Refer to the README.md in this directory for more information on the stats lock
func (db *Database) GetOrCreateStatsLock(
	ctx context.Context, stakingTxHashHex string, txType string,
) (*model.StatsLockDocument, error) {
	client := db.Client.Database(db.DbName).Collection(model.StatsLockCollection)
	id := constructStatsLockId(stakingTxHashHex, txType)
	filter := bson.M{"_id": id}
	// Define the default document to be inserted if not found
	// This setOnInsert will only be applied if the document is not found
	update := bson.M{
		"$setOnInsert": model.NewStatsLockDocument(
			id,
			false,
			false,
			false,
		),
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var result model.StatsLockDocument
	err := client.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// IncrementOverallStats increments the overall stats for the given staking tx hash.
// This method is idempotent, only the first call will be processed. Otherwise it will return a notFoundError for duplicates
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) IncrementOverallStats(
	ctx context.Context, stakingTxHashHex string, amount uint64,
) error {
	upsertUpdate := bson.M{
		"$inc": bson.M{
			"active_tvl":         int64(amount),
			"total_tvl":          int64(amount),
			"active_delegations": 1,
			"total_delegations":  1,
		},
	}
	return db.updateOverallStats(ctx, types.Active.ToString(), stakingTxHashHex, upsertUpdate)
}

// SubtractOverallStats decrements the overall stats for the given staking tx hash
// This method is idempotent, only the first call will be processed. Otherwise it will return a notFoundError for duplicates
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) SubtractOverallStats(
	ctx context.Context, stakingTxHashHex string, amount uint64,
) error {
	upsertUpdate := bson.M{
		"$inc": bson.M{
			"active_tvl":         -int64(amount),
			"active_delegations": -1,
		},
	}
	return db.updateOverallStats(ctx, types.Unbonded.ToString(), stakingTxHashHex, upsertUpdate)
}

// GetOverallStats fetches the overall stats from all the shards and sums them up
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) GetOverallStats(ctx context.Context) (*model.OverallStatsDocument, error) {
	// The collection is sharded by the _id field, so we need to query all the shards
	var shardsId []string
	for i := 0; i < int(db.cfg.LogicalShardCount); i++ {
		shardsId = append(shardsId, fmt.Sprintf("%d", i))
	}

	client := db.Client.Database(db.DbName).Collection(model.OverallStatsCollection)
	filter := bson.M{"_id": bson.M{"$in": shardsId}}
	cursor, err := client.Find(ctx, filter)
	if err != nil {
		return nil, err
	}

	var overallStats []model.OverallStatsDocument
	if err = cursor.All(ctx, &overallStats); err != nil {
		cursor.Close(ctx)
		return nil, err
	}
	cursor.Close(ctx)

	// Sum up the stats for the overall stats
	var result model.OverallStatsDocument
	for _, stats := range overallStats {
		result.ActiveTvl += stats.ActiveTvl
		result.TotalTvl += stats.TotalTvl
		result.ActiveDelegations += stats.ActiveDelegations
		result.TotalDelegations += stats.TotalDelegations
	}

	return &result, nil
}

// Generate the id for the overall stats document. Id is a random number ranged from 0-LogicalShardCount-1
// It's a logical shard to avoid locking the same field during concurrent writes
// The sharding number should never be reduced after roll out
func (db *Database) generateOverallStatsId() string {
	return fmt.Sprint(rand.Intn(int(db.cfg.LogicalShardCount)))
}

func (db *Database) updateOverallStats(ctx context.Context, state, stakingTxHashHex string, upsertUpdate primitive.M) error {
	overallStatsClient := db.Client.Database(db.DbName).Collection(model.OverallStatsCollection)

	// Start a session
	session, err := db.Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	// Define the work to be done in the transaction
	transactionWork := func(sessCtx mongo.SessionContext) (interface{}, error) {
		err := db.updateStatsLockByFieldName(sessCtx, stakingTxHashHex, state, "overall_stats")
		if err != nil {
			return nil, err
		}

		upsertFilter := bson.M{"_id": db.generateOverallStatsId()}

		_, err = overallStatsClient.UpdateOne(sessCtx, upsertFilter, upsertUpdate, options.Update().SetUpsert(true))
		if err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Execute the transaction
	_, err = session.WithTransaction(ctx, transactionWork)
	if err != nil {
		return err
	}

	return nil
}

func (db *Database) updateStatsLockByFieldName(ctx context.Context, stakingTxHashHex, state string, fieldName string) error {
	statsLockClient := db.Client.Database(db.DbName).Collection(model.StatsLockCollection)
	filter := bson.M{"_id": constructStatsLockId(stakingTxHashHex, state), fieldName: false}
	update := bson.M{"$set": bson.M{fieldName: true}}
	result, err := statsLockClient.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return &NotFoundError{
			Key:     stakingTxHashHex,
			Message: "document already processed or does not exist",
		}
	}
	return nil
}

func constructStatsLockId(stakingTxHashHex, state string) string {
	return stakingTxHashHex + ":" + state
}

// IncrementFinalityProviderStats increments the finality provider stats for the given staking tx hash
// This method is idempotent, only the first call will be processed. Otherwise it will return a notFoundError for duplicates
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) IncrementFinalityProviderStats(
	ctx context.Context, stakingTxHashHex, fpPkHex string, amount uint64,
) error {
	upsertUpdate := bson.M{
		"$inc": bson.M{
			"active_tvl":         int64(amount),
			"total_tvl":          int64(amount),
			"active_delegations": 1,
			"total_delegations":  1,
		},
	}
	return db.updateFinalityProviderStats(ctx, types.Active.ToString(), stakingTxHashHex, fpPkHex, upsertUpdate)
}

// SubtractFinalityProviderStats decrements the finality provider stats for the given provider pk hex
// This method is idempotent, only the first call will be processed. Otherwise it will return a notFoundError for duplicates
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) SubtractFinalityProviderStats(
	ctx context.Context, stakingTxHashHex, fpPkHex string, amount uint64,
) error {
	upsertUpdate := bson.M{
		"$inc": bson.M{
			"active_tvl":         -int64(amount),
			"active_delegations": -1,
		},
	}
	return db.updateFinalityProviderStats(ctx, types.Unbonded.ToString(), stakingTxHashHex, fpPkHex, upsertUpdate)
}

// FindFinalityProviderStatsByPkHex finds the finality provider stats for the given finality provider pk hex
// This method queries all the shards and sums up the stats
// Refer to the README.md in this directory for more information on the sharding logic
func (db *Database) FindFinalityProviderStatsByPkHex(ctx context.Context, pkHex []string) (map[string]model.FinalityProviderStatsDocument, error) {
	client := db.Client.Database(db.DbName).Collection(model.FinalityProviderStatsCollection)
	finalityProvidersMap := make(map[string]model.FinalityProviderStatsDocument)

	batchSize := int(db.cfg.DbBatchSizeLimit)
	for i := 0; i < len(pkHex); i += batchSize {
		end := i + batchSize
		if end > len(pkHex) {
			end = len(pkHex)
		}
		batch := pkHex[i:end]

		filter := bson.M{"_id": bson.M{"$in": db.getAllShardedFinalityProviderId(batch)}}
		cursor, err := client.Find(ctx, filter)
		if err != nil {
			return nil, err
		}

		var shardedFinalityProvidersStats []model.FinalityProviderStatsDocument
		if err = cursor.All(ctx, &shardedFinalityProvidersStats); err != nil {
			cursor.Close(ctx)
			return nil, err
		}
		cursor.Close(ctx)

		// Sum up the stats for the finality provider
		for _, fp := range shardedFinalityProvidersStats {
			// Retrieve the finality provider pk hex from the id.
			fpPkHex, err := extractFinalityProviderPkHexFromStatsId(fp.Id)
			if err != nil {
				return nil, err
			}
			if existingFp, ok := finalityProvidersMap[fpPkHex]; ok {
				existingFp.ActiveTvl += fp.ActiveTvl
				existingFp.TotalTvl += fp.TotalTvl
				existingFp.ActiveDelegations += fp.ActiveDelegations
				existingFp.TotalDelegations += fp.TotalDelegations

				finalityProvidersMap[fpPkHex] = existingFp
			} else {
				finalityProvidersMap[fpPkHex] = model.FinalityProviderStatsDocument{
					ActiveTvl:         fp.ActiveTvl,
					TotalTvl:          fp.TotalTvl,
					ActiveDelegations: fp.ActiveDelegations,
					TotalDelegations:  fp.TotalDelegations,
				}
			}
		}
	}

	return finalityProvidersMap, nil
}

func (db *Database) updateFinalityProviderStats(ctx context.Context, state, stakingTxHashHex, fpPkHex string, upsertUpdate primitive.M) error {
	client := db.Client.Database(db.DbName).Collection(model.FinalityProviderStatsCollection)

	// Start a session
	session, err := db.Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	transactionWork := func(sessCtx mongo.SessionContext) (interface{}, error) {
		err := db.updateStatsLockByFieldName(sessCtx, stakingTxHashHex, state, "finality_provider_stats")
		if err != nil {
			return nil, err
		}

		upsertFilter := bson.M{"_id": db.generateFinalityProviderStatsId(fpPkHex)}

		_, err = client.UpdateOne(sessCtx, upsertFilter, upsertUpdate, options.Update().SetUpsert(true))
		if err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Execute the transaction
	_, err = session.WithTransaction(ctx, transactionWork)
	if err != nil {
		return err
	}

	return nil
}

// Genrate the id for the finality provider stats document.
// Id is a combination of finality provider pk hex and a random number ranged from 0-LogicalShardCount
// This is designed to avoid locking the same field during concurrent writes
func (db *Database) generateFinalityProviderStatsId(finalityProviderPkHex string) string {
	randomShardNum := uint64(rand.Intn(int(db.cfg.LogicalShardCount)))
	return fmt.Sprintf("%s:%d", finalityProviderPkHex, randomShardNum)
}

func extractFinalityProviderPkHexFromStatsId(id string) (string, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid id format: %s", id)
	}
	return parts[0], nil
}

// Get the finality provider stats document id for all the shards
func (db *Database) getAllShardedFinalityProviderId(finalityProviderPkHex []string) []string {
	var ids []string
	for _, fpPkHex := range finalityProviderPkHex {
		for i := 0; i < int(db.cfg.LogicalShardCount); i++ {
			ids = append(ids, fmt.Sprintf("%s:%d", fpPkHex, i))
		}
	}
	return ids
}