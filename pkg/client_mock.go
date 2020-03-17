package pkg

type mockClient struct {
}

func NewMock() (Client, error) {
	return &mockClient{}, nil
}

func (t *mockClient) Ebs() EbsClient {
	return &mockEbsClient{
		ebs: make(map[string]*ebsInfo),
	}
}

func (t *mockClient) Elb() ElbClient {
	// TODO: impl
	return nil
}
