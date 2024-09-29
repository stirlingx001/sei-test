package main

import (
	"context"
	"errors"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	types2 "github.com/sei-protocol/sei-chain/x/evm/types"
	"github.com/tendermint/crypto/sha3"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto/merkle"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	"github.com/tendermint/tendermint/rpc/coretypes"
	tendermint_type "github.com/tendermint/tendermint/types"
	"strings"
)

func FetchBlock(cli *rpchttp.HTTP, height int64) *coretypes.ResultBlock {

	fmt.Printf("fetch block from rpc: %v\n", height)

	block, err := cli.Block(context.Background(), &height)
	if err != nil {
		fmt.Printf("Failed to get block: %v\n", err)
		panic(err)
	}
	return block
}

func FindTx(client *rpchttp.HTTP, block *coretypes.ResultBlock, evmTxHash string) (*tendermint_type.Tx, error) {
	for _, tx := range block.Block.Data.Txs {
		exampleTxRes, err := client.Tx(context.Background(), tx.Hash(), true)
		if err != nil {
			return nil, err
		}
		if exampleTxRes.TxResult.EvmTxInfo == nil {
			continue
		}
		if exampleTxRes.TxResult.EvmTxInfo.TxHash == evmTxHash {
			txMsgData := &sdk.TxMsgData{}
			err = txMsgData.Unmarshal(exampleTxRes.TxResult.Data)
			if err != nil {
				return nil, err
			}
			evmTxRes := &types2.MsgEVMTransactionResponse{}
			for _, msg := range txMsgData.Data {
				if strings.Contains(msg.MsgType, "MsgEVMTransaction") {
					err := evmTxRes.Unmarshal(msg.Data)
					if err != nil {
						return nil, err
					}
				}
			}

			return &tx, nil
		}
	}
	return nil, errors.New("not found")

}

func GenMerkletreeTxResultProof(client *rpchttp.HTTP, hash []byte) (merkle.Proof, *abci.ResponseDeliverTx, []byte, []byte, error) {
	//get the tx by txhash
	txRes, err := client.Tx(context.Background(), hash, true)
	if err != nil {
		return merkle.Proof{}, nil, nil, nil, err
	}
	//get the block by tx height
	block, err := client.Block(context.Background(), &txRes.Height)
	if err != nil {
		return merkle.Proof{}, nil, nil, nil, err
	}
	//check the tx is in the block
	if string(block.Block.Data.Txs[txRes.Index].Hash()) != string(hash) {
		panic("tx is not in the block")
	}
	//prepare next block to get the rootHash, check the parentHash of next block is equal to the current blockhash
	nextHeight := txRes.Height + 1
	nextBlock, err := client.Block(context.Background(), &nextHeight)
	if err != nil {
		return merkle.Proof{}, nil, nil, nil, err
	}
	if string(nextBlock.Block.Header.LastBlockID.Hash) != string(block.BlockID.Hash) {
		return merkle.Proof{}, nil, nil, nil, errors.New("next block is not the parent block")
	}

	//get the txResultProof by blockResults
	blockRes, err := client.BlockResults(context.Background(), &txRes.Height)
	if err != nil {
		//panic(err)
		return merkle.Proof{}, nil, nil, nil, err
	}
	newResults := tendermint_type.NewResults(convertTxsResults(blockRes.TxsResults))
	txResult := newResults[txRes.Index]
	txResultProof := newResults.ProveResult(int(txRes.Index))
	encodeTxResult, err := txResult.Marshal()
	if err != nil {
		panic(err)
	}

	return txResultProof, txResult, nextBlock.Block.Header.LastResultsHash, encodeTxResult, nil
}

func convertTxsResults(txsResults []*abci.ExecTxResult) []*abci.ResponseDeliverTx {
	res := make([]*abci.ResponseDeliverTx, len(txsResults))
	for i, v := range txsResults {
		res[i] = &abci.ResponseDeliverTx{
			Code:      v.Code,
			Data:      v.Data,
			Log:       v.Log,
			Info:      v.Info,
			GasWanted: v.GasWanted,
			GasUsed:   v.GasUsed,
			Events:    v.Events,
			Codespace: v.Codespace,
			EvmTxInfo: v.EvmTxInfo,
		}
	}
	return res
}

func ParseResponse(txResult *abci.ResponseDeliverTx) (string, *types2.MsgEVMTransactionResponse, error) {
	txMsgData := &sdk.TxMsgData{}
	err := txMsgData.Unmarshal(txResult.Data)
	if err != nil {
		return "", nil, err
	}
	//msgType := ""
	txResponse := &types2.MsgEVMTransactionResponse{}
	for _, msg := range txMsgData.Data {
		if strings.Contains(msg.MsgType, "MsgEVMTransaction") {
			//msgType = msg.MsgType
			err := txResponse.Unmarshal(msg.Data)
			if err != nil {
				return "", nil, err
			}
			break
		}
	}

	data1, _ := txResponse.Marshal()
	data2, _ := txMsgData.Marshal()
	data3, _ := txResult.Marshal()

	fmt.Printf("data1: %x\n", data1)
	fmt.Printf("data2: %x\n", data2)
	fmt.Printf("leaf_data: %x\n", data3)
	leafHash := sha3.Sum256(data3)
	fmt.Printf("leafHash: %x\n", leafHash)

	return "", nil, errors.New("not found")
}

func test() {
	url := "https://rpc-testnet.sei-apis.com:443"
	client, err := rpchttp.New(url)
	if err != nil {
		fmt.Errorf("failed to connect to the RPC server: %v", err)
		panic(err)
	}

	evmTxHash := common.HexToHash("0xa0b90d9817a1e9b546e996062940d26a3a7b26ebeb67b00742f0c311da9fcfcf")
	height := int64(119897376)
	curBlock := FetchBlock(client, height)

	tx, err := FindTx(client, curBlock, evmTxHash.String())
	if err != nil {
		panic(err)
	}

	fmt.Printf("tx: %x\n", tx.Hash())

	proof, txResult, rootHash, _, err := GenMerkletreeTxResultProof(client, tx.Hash())
	if err != nil {
		fmt.Printf("GenMerkletreeTxResultProof: %v\n", err)
		panic(err)
	}
	fmt.Printf("root: %x\n", rootHash)
	fmt.Printf("proof: %v\n", proof.String())

	ParseResponse(txResult)
}

func main() {
	test()
}
