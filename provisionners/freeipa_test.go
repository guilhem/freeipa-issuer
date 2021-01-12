package provisioners

import "testing"

func Test_formatCertificate(t *testing.T) {
	type args struct {
		cert string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "no header no footer",
			args: args{
				cert: "cert",
			},
			want: "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----",
		},
		{
			name: "with header no footer",
			args: args{
				cert: "-----BEGIN CERTIFICATE-----\ncert",
			},
			want: "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----",
		},
		{
			name: "no header with footer",
			args: args{
				cert: "cert\n-----END CERTIFICATE-----",
			},
			want: "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----",
		},
		{
			name: "with header with footer",
			args: args{
				cert: "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----",
			},
			want: "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCertificate(tt.args.cert); got != tt.want {
				t.Errorf("formatCertificate() = %v, want %v", got, tt.want)
			}
		})
	}
}
