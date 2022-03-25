package starportcmd

import (
	"fmt"
	"os"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/tendermint/spn/pkg/chainid"
	campaigntypes "github.com/tendermint/spn/x/campaign/types"
	"github.com/tendermint/starport/starport/pkg/clispinner"
	"github.com/tendermint/starport/starport/pkg/o"
	"github.com/tendermint/starport/starport/services/network"
	"github.com/tendermint/starport/starport/services/network/networkchain"
)

const (
	flagTag          = "tag"
	flagBranch       = "branch"
	flagHash         = "hash"
	flagGenesis      = "genesis"
	flagCampaign     = "campaign"
	flagNoCheck      = "no-check"
	flagChainID      = "chain-id"
	flagMainnet      = "mainnet"
	flagRewardCoins  = "reward.coins"
	flagRewardHeight = "reward.height"
)

// NewNetworkChainPublish returns a new command to publish a new chain to start a new network.
func NewNetworkChainPublish() *cobra.Command {
	c := &cobra.Command{
		Use:   "publish [source-url]",
		Short: "Publish a new chain to start a new network",
		Args:  cobra.ExactArgs(1),
		RunE:  networkChainPublishHandler,
	}

	c.Flags().String(flagBranch, "", "Git branch to use for the repo")
	c.Flags().String(flagTag, "", "Git tag to use for the repo")
	c.Flags().String(flagHash, "", "Git hash to use for the repo")
	c.Flags().String(flagGenesis, "", "URL to a custom Genesis")
	c.Flags().String(flagChainID, "", "Chain ID to use for this network")
	c.Flags().Uint64(flagCampaign, 0, "Campaign ID to use for this network")
	c.Flags().Bool(flagNoCheck, false, "Skip verifying chain's integrity")
	// FIXME: total shares cannot be set if the campaign doesn't have dynamic shares.
	// TODO: we should update the SPN to accept dynamic shares before enabling this flag.
	// c.Flags().String(flagCampaignTotalShares, "", "Add a total shares to the campaign")
	c.Flags().String(flagCampaignMetadata, "", "Add a campaign metadata")
	c.Flags().String(flagCampaignTotalShares, "", "Add a shares supply for the campaign")
	c.Flags().String(flagCampaignTotalSupply, "", "Add a total of the mainnet of a campaign")
	c.Flags().Bool(flagMainnet, false, "Initialize a mainnet campaign")
	c.Flags().String(flagRewardCoins, "", "Reward coins")
	c.Flags().Int64(flagRewardHeight, 0, "Last reward height")
	c.Flags().AddFlagSet(flagNetworkFrom())
	c.Flags().AddFlagSet(flagSetKeyringBackend())
	c.Flags().AddFlagSet(flagSetHome())
	c.Flags().AddFlagSet(flagSetYes())

	return c
}

func networkChainPublishHandler(cmd *cobra.Command, args []string) error {
	var (
		source                    = args[0]
		tag, _                    = cmd.Flags().GetString(flagTag)
		branch, _                 = cmd.Flags().GetString(flagBranch)
		hash, _                   = cmd.Flags().GetString(flagHash)
		genesisURL, _             = cmd.Flags().GetString(flagGenesis)
		chainID, _                = cmd.Flags().GetString(flagChainID)
		campaign, _               = cmd.Flags().GetUint64(flagCampaign)
		noCheck, _                = cmd.Flags().GetBool(flagNoCheck)
		campaignMetadata, _       = cmd.Flags().GetString(flagCampaignMetadata)
		campaignTotalSharesStr, _ = cmd.Flags().GetString(flagCampaignTotalShares)
		campaignTotalSupplyStr, _ = cmd.Flags().GetString(flagCampaignTotalSupply)
		isMainnet, _              = cmd.Flags().GetBool(flagMainnet)
		rewardCoinsStr, _         = cmd.Flags().GetString(flagRewardCoins)
		rewardDuration, _         = cmd.Flags().GetInt64(flagRewardHeight)
	)

	if campaign != 0 && campaignTotalSupplyStr != "" {
		return fmt.Errorf("%s and %s flags cannot be set together", flagCampaign, flagCampaignTotalSupply)
	}
	if isMainnet {
		if campaign == 0 && campaignTotalSupplyStr == "" {
			return fmt.Errorf(
				"%s flag requires one of the %s or %s flags to be set",
				flagMainnet,
				flagCampaign,
				flagCampaignTotalSupply,
			)
		}
		if chainID == "" {
			return fmt.Errorf("%s flag requires the %s flag", flagMainnet, flagChainID)
		}
	}

	if chainID != "" {
		chainName, _, err := chainid.ParseGenesisChainID(chainID)
		if err != nil {
			return errors.Wrapf(err, "invalid chain id: %s", chainID)
		}
		if err := chainid.CheckChainName(chainName); err != nil {
			return errors.Wrapf(err, "invalid chain id name: %s", chainName)
		}
	}

	totalShares, err := campaigntypes.NewShares(campaignTotalSharesStr)
	if err != nil {
		return err
	}

	rewardCoins, err := sdk.ParseCoinsNormalized(rewardCoinsStr)
	if err != nil {
		return err
	}

	if (!rewardCoins.Empty() && rewardDuration == 0) ||
		(rewardCoins.Empty() && rewardDuration > 0) {
		return fmt.Errorf("%s and %s flags must be provided together", flagRewardCoins, flagRewardHeight)
	}

	nb, err := newNetworkBuilder(cmd)
	if err != nil {
		return err
	}
	defer nb.Cleanup()

	// use source from chosen target.
	var sourceOption o.Option[networkchain.Source]

	switch {
	case tag != "":
		sourceOption = networkchain.SourceRemoteTag(source, tag)
	case branch != "":
		sourceOption = networkchain.SourceRemoteBranch(source, branch)
	case hash != "":
		sourceOption = networkchain.SourceRemoteHash(source, hash)
	default:
		sourceOption = networkchain.SourceRemote(source)
	}

	// init in a temp dir.
	homeDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(homeDir)

	initOptions := o.Options(
		networkchain.WithHome(homeDir),
	).
		AddC(genesisURL != "", networkchain.WithGenesisFromURL(genesisURL)) // use custom genesis from url if given.

	// init the chain.
	c, err := nb.Chain(sourceOption, initOptions.O()...)
	if err != nil {
		return err
	}

	publishOptions := o.
		Options(
			network.WithMetadata(campaignMetadata),
		).
		AddC(genesisURL != "", network.WithCustomGenesis(genesisURL)).
		AddC(chainID != "", network.WithChainID(chainID)). // use custom chain id if given.
		AddC(isMainnet, network.Mainnet()).
		AddC(!totalShares.Empty(), network.WithTotalShares(totalShares))

	if campaign != 0 {
		publishOptions.Add(network.WithCampaign(campaign))
	} else if campaignTotalSupplyStr != "" {
		totalSupply, err := sdk.ParseCoinsNormalized(campaignTotalSupplyStr)
		if err != nil {
			return err
		}

		if !totalSupply.Empty() {
			publishOptions.Add(network.WithTotalSupply(totalSupply))
		}
	}

	if noCheck {
		publishOptions.Add(network.WithNoCheck())
	} else if err := c.Init(cmd.Context()); err != nil { // initialize the chain for checking.
		return err
	}

	nb.Spinner.SetText("Publishing...")
	nb.Spinner.Start()

	n, err := nb.Network()
	if err != nil {
		return err
	}

	launchID, campaignID, mainnetID, err := n.Publish(cmd.Context(), c, publishOptions.O()...)
	if err != nil {
		return err
	}

	if !rewardCoins.Empty() && rewardDuration > 0 {
		if err := n.SetReward(launchID, rewardDuration, rewardCoins); err != nil {
			return err
		}
	}

	nb.Spinner.Stop()

	fmt.Printf("%s Network published \n", clispinner.OK)
	fmt.Printf("%s Launch ID: %d \n", clispinner.Bullet, launchID)
	fmt.Printf("%s Campaign ID: %d \n", clispinner.Bullet, campaignID)
	if isMainnet {
		fmt.Printf("%s Mainnet ID: %d \n", clispinner.Bullet, mainnetID)
	}

	return nil
}
