package v1service

import (
	"context"
	"net/http"

	"github.com/babylonlabs-io/staking-api-service/internal/shared/db"
	"github.com/babylonlabs-io/staking-api-service/internal/shared/types"
	"github.com/babylonlabs-io/staking-api-service/internal/shared/utils"
	v1dbclient "github.com/babylonlabs-io/staking-api-service/internal/v1/db/client"
	v1model "github.com/babylonlabs-io/staking-api-service/internal/v1/db/model"
	"github.com/rs/zerolog/log"
)

type TransactionPublic struct {
	TxHex          string `json:"tx_hex"`
	OutputIndex    uint64 `json:"output_index"`
	StartTimestamp string `json:"start_timestamp"`
	StartHeight    uint64 `json:"start_height"`
	TimeLock       uint64 `json:"timelock"`
}

type DelegationPublic struct {
	StakingTxHashHex      string             `json:"staking_tx_hash_hex"`
	StakerPkHex           string             `json:"staker_pk_hex"`
	FinalityProviderPkHex string             `json:"finality_provider_pk_hex"`
	State                 string             `json:"state"`
	StakingValue          uint64             `json:"staking_value"`
	StakingTx             *TransactionPublic `json:"staking_tx"`
	UnbondingTx           *TransactionPublic `json:"unbonding_tx,omitempty"`
	IsOverflow            bool               `json:"is_overflow"`
}

func FromDelegationDocument(d *v1model.DelegationDocument) DelegationPublic {
	delPublic := DelegationPublic{
		StakingTxHashHex:      d.StakingTxHashHex,
		StakerPkHex:           d.StakerPkHex,
		FinalityProviderPkHex: d.FinalityProviderPkHex,
		StakingValue:          d.StakingValue,
		State:                 d.State.ToString(),
		StakingTx: &TransactionPublic{
			TxHex:          d.StakingTx.TxHex,
			OutputIndex:    d.StakingTx.OutputIndex,
			StartTimestamp: utils.ParseTimestampToIsoFormat(d.StakingTx.StartTimestamp),
			StartHeight:    d.StakingTx.StartHeight,
			TimeLock:       d.StakingTx.TimeLock,
		},
		IsOverflow: d.IsOverflow,
	}

	// Add unbonding transaction if it exists
	if d.UnbondingTx != nil && d.UnbondingTx.TxHex != "" {
		delPublic.UnbondingTx = &TransactionPublic{
			TxHex:          d.UnbondingTx.TxHex,
			OutputIndex:    d.UnbondingTx.OutputIndex,
			StartTimestamp: utils.ParseTimestampToIsoFormat(d.UnbondingTx.StartTimestamp),
			StartHeight:    d.UnbondingTx.StartHeight,
			TimeLock:       d.UnbondingTx.TimeLock,
		}
	}
	return delPublic
}

func (s *V1Service) DelegationsByStakerPk(
	ctx context.Context, stakerPk string,
	state types.DelegationState, pageToken string,
) ([]DelegationPublic, string, *types.Error) {
	filter := &v1dbclient.DelegationFilter{}
	if state != "" {
		filter = &v1dbclient.DelegationFilter{
			States: []types.DelegationState{state},
		}
	}

	resultMap, err := s.Service.DbClients.V1DBClient.FindDelegationsByStakerPk(ctx, stakerPk, filter, pageToken)
	if err != nil {
		if db.IsInvalidPaginationTokenError(err) {
			log.Ctx(ctx).Warn().Err(err).Msg("Invalid pagination token when fetching delegations by staker pk")
			return nil, "", types.NewError(http.StatusBadRequest, types.BadRequest, err)
		}
		log.Ctx(ctx).Error().Err(err).Msg("Failed to find delegations by staker pk")
		return nil, "", types.NewInternalServiceError(err)
	}
	var delegations []DelegationPublic = make([]DelegationPublic, 0, len(resultMap.Data))
	for _, d := range resultMap.Data {
		delegations = append(delegations, FromDelegationDocument(&d))
	}
	return delegations, resultMap.PaginationToken, nil
}

// SaveActiveStakingDelegation saves the active staking delegation to the database.
func (s *V1Service) SaveActiveStakingDelegation(
	ctx context.Context, txHashHex, stakerPkHex, finalityProviderPkHex string,
	value, startHeight uint64, stakingTimestamp int64, timeLock, stakingOutputIndex uint64,
	stakingTxHex string, isOverflow bool,
) *types.Error {
	err := s.Service.DbClients.V1DBClient.SaveActiveStakingDelegation(
		ctx, txHashHex, stakerPkHex, finalityProviderPkHex, stakingTxHex,
		value, startHeight, timeLock, stakingOutputIndex, stakingTimestamp, isOverflow,
	)
	if err != nil {
		if ok := db.IsDuplicateKeyError(err); ok {
			log.Ctx(ctx).Warn().Err(err).Msg("Skip the active staking event as it already exists in the database")
			return nil
		}
		log.Ctx(ctx).Error().Err(err).Msg("Failed to save active staking delegation")
		return types.NewInternalServiceError(err)
	}
	return nil
}

func (s *V1Service) IsDelegationPresent(ctx context.Context, txHashHex string) (bool, *types.Error) {
	delegation, err := s.Service.DbClients.V1DBClient.FindDelegationByTxHashHex(ctx, txHashHex)
	if err != nil {
		if db.IsNotFoundError(err) {
			return false, nil
		}
		log.Ctx(ctx).Error().Err(err).Msg("Failed to find delegation by tx hash hex")
		return false, types.NewInternalServiceError(err)
	}
	if delegation != nil {
		return true, nil
	}

	return false, nil
}

func (s *V1Service) GetDelegation(ctx context.Context, txHashHex string) (*v1model.DelegationDocument, *types.Error) {
	delegation, err := s.Service.DbClients.V1DBClient.FindDelegationByTxHashHex(ctx, txHashHex)
	if err != nil {
		if db.IsNotFoundError(err) {
			log.Ctx(ctx).Warn().Err(err).Str("stakingTxHash", txHashHex).Msg("Staking delegation not found")
			return nil, types.NewErrorWithMsg(http.StatusNotFound, types.NotFound, "staking delegation not found, please retry")
		}
		log.Ctx(ctx).Error().Err(err).Msg("Failed to find delegation by tx hash hex")
		return nil, types.NewInternalServiceError(err)
	}
	return delegation, nil
}

func (s *V1Service) CheckStakerHasActiveDelegationByPk(
	ctx context.Context, stakerPk string, afterTimestamp int64,
) (bool, *types.Error) {
	filter := &v1dbclient.DelegationFilter{
		States:         []types.DelegationState{types.Active},
		AfterTimestamp: afterTimestamp,
	}
	hasDelegation, err := s.Service.DbClients.V1DBClient.CheckDelegationExistByStakerPk(
		ctx, stakerPk, filter,
	)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to check if staker has active delegation")
		return false, types.NewInternalServiceError(err)
	}
	return hasDelegation, nil
}
