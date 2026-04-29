package cdp

import "context"

type TargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	Attached bool   `json:"attached"`
}

func ListTargets(ctx context.Context, endpoint string) ([]TargetInfo, error) {
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.CloseNormal()

	return ListTargetsWithClient(ctx, client)
}

func ListTargetsWithClient(ctx context.Context, client CommandClient) ([]TargetInfo, error) {
	var result struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := client.Call(ctx, "Target.getTargets", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.TargetInfos, nil
}

func TargetInfoWithClient(ctx context.Context, client CommandClient, targetID string) (TargetInfo, error) {
	var result struct {
		TargetInfo TargetInfo `json:"targetInfo"`
	}
	if err := client.Call(ctx, "Target.getTargetInfo", map[string]any{"targetId": targetID}, &result); err != nil {
		return TargetInfo{}, err
	}
	return result.TargetInfo, nil
}
