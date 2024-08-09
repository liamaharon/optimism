package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"

	builderSpec "github.com/attestantio/go-builder-client/spec"
	consensusspec "github.com/attestantio/go-eth2-client/spec"

	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/pkg/errors"
)

var (
	errHTTPErrorResponse = errors.New("HTTP error response")
)

const PathGetPayload = "/eth/v1/builder/payload"

type BuilderAPIConfig struct {
	Enabled  bool
	Endpoint string
}

func BuilderAPIDefaultConfig() *BuilderAPIConfig {
	return &BuilderAPIConfig{
		Enabled:  false,
		Endpoint: "",
	}
}

type BuilderAPIClient struct {
	log        log.Logger
	config     *BuilderAPIConfig
	httpClient *client.BasicHTTPClient
}

func NewBuilderAPIClient(log log.Logger, config *BuilderAPIConfig) *BuilderAPIClient {
	httpClient := client.NewBasicHTTPClient(config.Endpoint, log)

	return &BuilderAPIClient{
		httpClient: httpClient,
		config:     config,
		log:        log,
	}
}

func (s *BuilderAPIClient) Enabled() bool {
	return s.config.Enabled
}

type httpErrorResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *BuilderAPIClient) GetPayload(ctx context.Context, ref eth.L2BlockRef, log log.Logger) (*eth.ExecutionPayloadEnvelope, *big.Int, error) {
	responsePayload := new(builderSpec.VersionedSubmitBlockRequest)
	slot := ref.Number + 1
	parentHash := ref.Hash
	url := fmt.Sprintf("%s/%d/%s", PathGetPayload, slot, parentHash.String())
	header := http.Header{"Accept": {"application/json"}}
	resp, err := s.httpClient.Get(ctx, url, nil, header)
	if err != nil {
		return nil, nil, err
	}

	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, errHTTPErrorResponse
	}

	if err := json.Unmarshal(bodyBytes, responsePayload); err != nil {
		return nil, nil, err
	}

	if responsePayload.Version != consensusspec.DataVersionDeneb {
		return nil, nil, fmt.Errorf("unsupported data version %v", responsePayload.Version)
	}

	profit := responsePayload.Deneb.Message.Value.ToBig()
	envelope, err := versionedExecutionPayloadToExecutionPayloadEnvelope(responsePayload)
	if err != nil {
		return nil, nil, err
	}
	return envelope, profit, nil
}

func versionedExecutionPayloadToExecutionPayloadEnvelope(resp *builderSpec.VersionedSubmitBlockRequest) (*eth.ExecutionPayloadEnvelope, error) {
	if resp.Version != consensusspec.DataVersionDeneb {
		return nil, fmt.Errorf("unsupported data version %v", resp.Version)
	}

	payload := resp.Deneb.ExecutionPayload
	txs := make([]eth.Data, len(payload.Transactions))

	for i, tx := range payload.Transactions {
		txs[i] = eth.Data(tx)
	}

	withdrawals := make([]*types.Withdrawal, len(payload.Withdrawals))
	for i, withdrawal := range payload.Withdrawals {
		withdrawals[i] = &types.Withdrawal{
			Index:     uint64(withdrawal.Index),
			Validator: uint64(withdrawal.ValidatorIndex),
			Address:   common.BytesToAddress(withdrawal.Address[:]),
			Amount:    uint64(withdrawal.Amount),
		}
	}

	ws := types.Withdrawals(withdrawals)

	blobGasUsed := eth.Uint64Quantity(payload.BlobGasUsed)
	excessBlobGas := eth.Uint64Quantity(payload.ExcessBlobGas)

	envelope := &eth.ExecutionPayloadEnvelope{
		ExecutionPayload: &eth.ExecutionPayload{
			ParentHash:    common.Hash(payload.ParentHash),
			FeeRecipient:  common.Address(payload.FeeRecipient),
			StateRoot:     eth.Bytes32(payload.StateRoot),
			ReceiptsRoot:  eth.Bytes32(payload.ReceiptsRoot),
			LogsBloom:     eth.Bytes256(payload.LogsBloom),
			PrevRandao:    eth.Bytes32(payload.PrevRandao),
			BlockNumber:   eth.Uint64Quantity(payload.BlockNumber),
			GasLimit:      eth.Uint64Quantity(payload.GasLimit),
			GasUsed:       eth.Uint64Quantity(payload.GasUsed),
			Timestamp:     eth.Uint64Quantity(payload.Timestamp),
			ExtraData:     eth.BytesMax32(payload.ExtraData),
			BaseFeePerGas: hexutil.U256(*payload.BaseFeePerGas),
			BlockHash:     common.BytesToHash(payload.BlockHash[:]),
			Transactions:  txs,
			Withdrawals:   &ws,
			BlobGasUsed:   &blobGasUsed,
			ExcessBlobGas: &excessBlobGas,
		},
		ParentBeaconBlockRoot: nil,
	}
	return envelope, nil
}
