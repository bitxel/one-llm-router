package core

import (
	"fmt"
	"hash/crc32"
	"sort"

	"github.com/user/one-llm-router/internal/domain"
)

const defaultVirtualNodes = 150

type ConsistentHashRouter struct {
	virtualNodes int
}

func NewConsistentHashRouter() *ConsistentHashRouter {
	return &ConsistentHashRouter{virtualNodes: defaultVirtualNodes}
}

type hashEntry struct {
	hash      uint32
	accountID int64
}

func (r *ConsistentHashRouter) Route(sessionKey string, activeAccounts []domain.UpstreamAccount) (domain.UpstreamAccount, error) {
	if len(activeAccounts) == 0 {
		return domain.UpstreamAccount{}, domain.ErrNoCapacity
	}
	if len(activeAccounts) == 1 {
		return activeAccounts[0], nil
	}

	accountMap := make(map[int64]domain.UpstreamAccount, len(activeAccounts))
	ring := make([]hashEntry, 0, len(activeAccounts)*r.virtualNodes)

	for _, acct := range activeAccounts {
		accountMap[acct.ID] = acct
		for i := range r.virtualNodes {
			key := fmt.Sprintf("%d#%d", acct.ID, i)
			h := crc32.ChecksumIEEE([]byte(key))
			ring = append(ring, hashEntry{hash: h, accountID: acct.ID})
		}
	}

	sort.Slice(ring, func(i, j int) bool {
		return ring[i].hash < ring[j].hash
	})

	keyHash := crc32.ChecksumIEEE([]byte(sessionKey))

	idx := sort.Search(len(ring), func(i int) bool {
		return ring[i].hash >= keyHash
	})
	if idx >= len(ring) {
		idx = 0
	}

	return accountMap[ring[idx].accountID], nil
}
