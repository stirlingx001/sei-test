package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	types2 "github.com/sei-protocol/sei-chain/x/evm/types"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto/merkle"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	"github.com/tendermint/tendermint/rpc/coretypes"
	tendermint_type "github.com/tendermint/tendermint/types"
	"strings"
)

var (
	leafPrefix  = []byte{0}
	innerPrefix = []byte{1}
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

func ParseAndVerify(txResult *abci.ResponseDeliverTx, proof merkle.Proof, root []byte, logIndex int) (string, *types2.MsgEVMTransactionResponse, error) {
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

	//data1, _ := txResponse.Marshal()
	//data2, _ := txMsgData.Marshal()
	leafData, _ := txResult.Marshal()

	//fmt.Printf("data1: %x\n", data1)
	//fmt.Printf("data2: %x\n", data2)
	fmt.Printf("leafdata: %x\n", leafData)
	fmt.Printf("leftHash: %x\n", sha256.Sum256(append(leafPrefix, leafData...)))

	err = proof.Verify(root, leafData)
	if err != nil {
		panic(err)
	}

	encodedLog, err := txResponse.Logs[logIndex].Marshal()
	if err != nil {
		panic(err)
	}
	pos := strings.Index(string(leafData), string(encodedLog))
	fmt.Printf("pos: %d, len: %d\n", pos, len(encodedLog))

	destLog := &types2.Log{}
	destLog.Unmarshal(leafData[pos : pos+len(encodedLog)])
	fmt.Printf("log: %+v\n", destLog)

	return "", nil, errors.New("not found")
}

func test() {
	url := "https://rpc-testnet.sei-apis.com:443"
	client, err := rpchttp.New(url)
	if err != nil {
		fmt.Errorf("failed to connect to the RPC server: %v", err)
		panic(err)
	}

	evmTxHash := common.HexToHash("0xac3580cbc872123146affd257141950c3a2285a48b00c9e25285c89e04f55fa4")
	height := int64(122593436)
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
	fmt.Printf("leafHash: %x\n", proof.LeafHash)

	ParseAndVerify(txResult, proof, rootHash, 2)
}

func main() {
	test()
}
