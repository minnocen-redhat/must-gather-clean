package fsutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePrivateACLListing(t *testing.T) {
	const path = `C:\Users\alice\owner-only file`
	accounts := []string{`EXAMPLE\alice`, `S-1-5-21-1000`}

	tests := []struct {
		name    string
		listing string
		wantErr bool
	}{
		{
			name: "path-prefixed owner entry",
			listing: path + ` EXAMPLE\alice:(OI)(CI)(F)` + "\r\n" +
				`Successfully processed 1 files; Failed processing 0 files`,
		},
		{
			name:    "indented owner entry",
			listing: "    EXAMPLE\\alice:(F)\r\n",
		},
		{
			name:    "owner SID",
			listing: path + ` S-1-5-21-1000:(F)` + "\r\n",
		},
		{
			name:    "account names are case insensitive",
			listing: path + ` example\ALICE:(F)` + "\r\n",
		},
		{
			name:    "other account",
			listing: path + ` BUILTIN\Administrators:(F)` + "\r\n",
			wantErr: true,
		},
		{
			name:    "owner plus other account",
			listing: path + " EXAMPLE\\alice:(F)\r\n" + `    NT AUTHORITY\SYSTEM:(F)` + "\r\n",
			wantErr: true,
		},
		{
			name:    "owner suffix in another account",
			listing: path + ` OTHER\EXAMPLE\alice:(F)` + "\r\n",
			wantErr: true,
		},
		{
			name:    "owner without full control",
			listing: path + ` EXAMPLE\alice:(R)` + "\r\n",
			wantErr: true,
		},
		{
			name:    "full-control text in path does not count",
			listing: path + `(F) EXAMPLE\alice:(R)` + "\r\n",
			wantErr: true,
		},
		{
			name:    "no ACL entries",
			listing: `Successfully processed 1 files; Failed processing 0 files`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrivateACLListing(path, tt.listing, accounts)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
