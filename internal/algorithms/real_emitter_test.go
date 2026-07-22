package algorithms

import "testing"

func TestContractSizingInteger(t *testing.T) {
	tests := []struct {
		name          string
		kellyRaw      float64
		priceCents    int64
		balanceCents  int64
		wantContracts int
		wantSpend     int64
	}{
		{
			name:          "fractional kelly floors down",
			kellyRaw:      3.7,
			priceCents:    50,
			balanceCents:  100000,
			wantContracts: 3,
			wantSpend:     150,
		},
		{
			name:          "exact integer passes through",
			kellyRaw:      4.0,
			priceCents:    25,
			balanceCents:  100000,
			wantContracts: 4,
			wantSpend:     100,
		},
		{
			name:          "sub-1 kelly produces no order",
			kellyRaw:      0.9,
			priceCents:    50,
			balanceCents:  100000,
			wantContracts: 0,
			wantSpend:     0,
		},
		{
			name:          "zero kelly produces no order",
			kellyRaw:      0,
			priceCents:    50,
			balanceCents:  100000,
			wantContracts: 0,
			wantSpend:     0,
		},
		{
			name:          "clamp to affordable integer",
			kellyRaw:      10.0,
			priceCents:    50,
			balanceCents:  300, // can afford 6 contracts
			wantContracts: 6,
			wantSpend:     300,
		},
		{
			name:          "sub-1 after clamp produces no order",
			kellyRaw:      10.0,
			priceCents:    50,
			balanceCents:  25, // can't afford even 1 at 50c
			wantContracts: 0,
			wantSpend:     0,
		},
		{
			name:          "spend equals contracts times price exactly",
			kellyRaw:      7.0,
			priceCents:    33,
			balanceCents:  100000,
			wantContracts: 7,
			wantSpend:     231,
		},
		{
			name:          "zero balance produces no order",
			kellyRaw:      5.0,
			priceCents:    50,
			balanceCents:  0,
			wantContracts: 0,
			wantSpend:     0,
		},
		{
			name:          "zero price produces no order",
			kellyRaw:      5.0,
			priceCents:    0,
			balanceCents:  100000,
			wantContracts: 0,
			wantSpend:     0,
		},
		{
			name:          "clamp lands exactly on affordable count",
			kellyRaw:      100.0,
			priceCents:    10,
			balanceCents:  1000,
			wantContracts: 100,
			wantSpend:     1000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contracts, spend := sizeRealOrder(tc.kellyRaw, tc.priceCents, tc.balanceCents)
			if contracts != tc.wantContracts {
				t.Errorf("contracts = %d, want %d", contracts, tc.wantContracts)
			}
			if spend != tc.wantSpend {
				t.Errorf("spend = %d, want %d", spend, tc.wantSpend)
			}
			// Invariant: spend == contracts * priceCents when contracts > 0.
			if contracts > 0 && spend != int64(contracts)*tc.priceCents {
				t.Errorf("spend %d != contracts %d * priceCents %d = %d",
					spend, contracts, tc.priceCents, int64(contracts)*tc.priceCents)
			}
			// Invariant: spend never exceeds balance.
			if spend > tc.balanceCents {
				t.Errorf("spend %d exceeds balance %d", spend, tc.balanceCents)
			}
		})
	}
}
