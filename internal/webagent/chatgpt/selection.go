package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	ThinkingCurrent = "current"
	ThinkingMiddle  = "middle"
	ThinkingHighest = "highest"
	ModelCurrent    = "current"
	ModelHighest    = "highest"
)

var thinkingLabelsAscending = []string{
	"Instant",
	"Instant 5.5",
	"Medium",
	"High",
	"Extra High",
	"Pro",
}

// SelectionPolicy keeps the public transport neutral by default. Entitlement-
// specific preferences belong in owner-only config or explicit command flags.
type SelectionPolicy struct {
	Thinking        string
	MinimumThinking string
	Model           string
}

func NormalizeSelectionPolicy(policy SelectionPolicy) (SelectionPolicy, error) {
	thinking, ok := normalizeThinkingPolicy(policy.Thinking, true)
	if !ok {
		return SelectionPolicy{}, fmt.Errorf(
			"unsupported ChatGPT thinking policy %q; use current, middle, instant, instant-5.5, medium, high, extra-high, pro, or highest",
			policy.Thinking,
		)
	}
	minimum := ""
	if strings.TrimSpace(policy.MinimumThinking) != "" {
		var minimumOK bool
		minimum, minimumOK = normalizeThinkingPolicy(
			policy.MinimumThinking,
			false,
		)
		if !minimumOK {
			return SelectionPolicy{}, fmt.Errorf(
				"unsupported ChatGPT minimum thinking level %q; use instant, instant-5.5, medium, high, extra-high, or pro",
				policy.MinimumThinking,
			)
		}
	}
	model := strings.TrimSpace(policy.Model)
	if model == "" {
		model = ModelCurrent
	}
	if len(model) > 120 || strings.ContainsAny(model, "\x00\r\n") {
		return SelectionPolicy{}, fmt.Errorf(
			"ChatGPT model must be a bounded single-line label",
		)
	}
	if strings.EqualFold(model, ModelCurrent) {
		model = ModelCurrent
	} else if strings.EqualFold(model, ModelHighest) {
		model = ModelHighest
	}
	return SelectionPolicy{
		Thinking:        thinking,
		MinimumThinking: minimum,
		Model:           model,
	}, nil
}

func normalizeThinkingPolicy(value string, allowPolicy bool) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.Join(strings.Fields(
		strings.NewReplacer("_", " ", "-", " ").Replace(normalized),
	), " ")
	if normalized == "" {
		return ThinkingCurrent, true
	}
	if allowPolicy {
		switch normalized {
		case ThinkingCurrent:
			return ThinkingCurrent, true
		case ThinkingMiddle:
			return ThinkingMiddle, true
		case ThinkingHighest:
			return ThinkingHighest, true
		}
	}
	switch normalized {
	case "instant":
		return "Instant", true
	case "instant 5.5":
		return "Instant 5.5", true
	case "medium":
		return "Medium", true
	case "high":
		return "High", true
	case "extra high", "xhigh":
		return "Extra High", true
	case "pro":
		return "Pro", true
	default:
		return "", false
	}
}

func thinkingRank(label string) int {
	for index, candidate := range thinkingLabelsAscending {
		if strings.EqualFold(strings.TrimSpace(label), candidate) {
			return index
		}
	}
	return -1
}

func thinkingSliderLabelsForRange(min, max int) []string {
	if min < 0 || max < min {
		return nil
	}
	// ChatGPT's current composer omits the legacy Instant position from its
	// five-stop slider. Keep the canonical six-label list for any surface that
	// still exposes all stops, while mapping the observed five-stop surface to
	// the five labels it actually renders. The range may have a non-zero
	// minimum, so reason from the number of stops rather than assuming 0.
	stopCount := max - min + 1
	if stopCount == len(thinkingLabelsAscending)-1 {
		return append([]string(nil), thinkingLabelsAscending[1:]...)
	}
	if stopCount != len(thinkingLabelsAscending) {
		return nil
	}
	return append([]string(nil), thinkingLabelsAscending...)
}

func thinkingSliderLabels(max int) []string {
	return thinkingSliderLabelsForRange(0, max)
}

func thinkingSliderTargetIndex(label string, max int) (int, bool) {
	labels := thinkingSliderLabels(max)
	for index, candidate := range labels {
		if strings.EqualFold(strings.TrimSpace(label), candidate) {
			return index, true
		}
	}
	return 0, false
}

func thinkingSliderTargetValue(label string, min, max int) (int, bool) {
	index, ok := thinkingSliderTargetIndexForRange(label, min, max)
	if !ok {
		return 0, false
	}
	return min + index, true
}

func thinkingSliderTargetIndexForRange(label string, min, max int) (int, bool) {
	labels := thinkingSliderLabelsForRange(min, max)
	for index, candidate := range labels {
		if strings.EqualFold(strings.TrimSpace(label), candidate) {
			return index, true
		}
	}
	return 0, false
}

func thinkingSliderMinimumValue(label string, min, max int) (int, bool) {
	labels := thinkingSliderLabelsForRange(min, max)
	if len(labels) == 0 {
		return 0, false
	}
	requestedRank := thinkingRank(label)
	if requestedRank < 0 {
		return 0, false
	}
	for index, candidate := range labels {
		if thinkingRank(candidate) >= requestedRank {
			return min + index, true
		}
	}
	return 0, false
}

func thinkingSliderMiddleValue(min, max int) (int, bool) {
	if min < 0 || max < min {
		return 0, false
	}
	return min + (max-min)/2, true
}

func thinkingAtOrAbove(selected, minimum string) bool {
	if minimum == "" {
		return thinkingRank(selected) >= 0
	}
	selectedRank := thinkingRank(selected)
	minimumRank := thinkingRank(minimum)
	return selectedRank >= 0 && minimumRank >= 0 && selectedRank >= minimumRank
}

type selectionPoint struct {
	Ready bool    `json:"ready"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type selectableOption struct {
	Label   string  `json:"label"`
	Checked bool    `json:"checked"`
	Ready   bool    `json:"ready"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
}

type selectionSurface struct {
	Editor              selectionPoint     `json:"editor"`
	ChatCount           int                `json:"chat_count"`
	WorkCount           int                `json:"work_count"`
	ChatSelected        bool               `json:"chat_selected"`
	Chat                selectionPoint     `json:"chat"`
	SpecializedCount    int                `json:"specialized_count"`
	PickerCount         int                `json:"picker_count"`
	Picker              selectionPoint     `json:"picker"`
	SelectedThinking    string             `json:"selected_thinking"`
	ThinkingMenuOpen    bool               `json:"thinking_menu_open"`
	ThinkingOptions     []selectableOption `json:"thinking_options"`
	ThinkingSliderReady bool               `json:"thinking_slider_ready"`
	ThinkingSliderMin   int                `json:"thinking_slider_min"`
	ThinkingSliderMax   int                `json:"thinking_slider_max"`
	ThinkingSliderValue int                `json:"thinking_slider_value"`
	ThinkingSliderLabel string             `json:"thinking_slider_label"`
	ModelTriggerCount   int                `json:"model_trigger_count"`
	ModelTrigger        selectionPoint     `json:"model_trigger"`
	ModelTriggerLabel   string             `json:"model_trigger_label"`
	ModelMenuOpen       bool               `json:"model_menu_open"`
	ModelOptions        []selectableOption `json:"model_options_provider_order"`
	SelectedModel       string             `json:"selected_model"`
}

// thinkingSelectionExpectation is the proof carried from reversible
// selection into the action-pending boundary. A visible label is sufficient
// for legacy menu controls; a slider/range control must retain its observed
// bounds and numeric target so a relabelled surface cannot silently weaken
// the Send guard.
type thinkingSelectionExpectation struct {
	Label                  string
	Minimum                string
	SliderMin              int
	SliderMax              int
	SliderTarget           int
	HasSliderTarget        bool
	MinimumSliderTarget    int
	HasMinimumSliderTarget bool
}

func (expectation thinkingSelectionExpectation) requiresThinkingMenu() bool {
	return expectation.HasSliderTarget || expectation.HasMinimumSliderTarget ||
		strings.TrimSpace(expectation.Minimum) != ""
}

func (expectation thinkingSelectionExpectation) sliderProofRequired() bool {
	return expectation.HasSliderTarget || expectation.HasMinimumSliderTarget
}

func (expectation thinkingSelectionExpectation) sliderRangeMatches(
	surface selectionSurface,
) bool {
	return surface.ThinkingSliderReady &&
		surface.ThinkingSliderMin == expectation.SliderMin &&
		surface.ThinkingSliderMax == expectation.SliderMax
}

func (expectation thinkingSelectionExpectation) matches(
	surface selectionSurface,
	menuRequired bool,
) bool {
	if surface.PickerCount != 1 {
		return false
	}
	if expectation.sliderProofRequired() {
		if !expectation.sliderRangeMatches(surface) ||
			(menuRequired && !surface.ThinkingMenuOpen) {
			return false
		}
		if expectation.HasSliderTarget &&
			surface.ThinkingSliderValue != expectation.SliderTarget {
			return false
		}
		return !expectation.HasMinimumSliderTarget ||
			surface.ThinkingSliderValue >= expectation.MinimumSliderTarget
	}
	if expectation.Label != "" && !strings.EqualFold(
		strings.TrimSpace(surface.SelectedThinking),
		strings.TrimSpace(expectation.Label),
	) {
		return false
	}
	if expectation.Minimum != "" {
		return thinkingAtOrAbove(
			surface.SelectedThinking,
			expectation.Minimum,
		) || thinkingAtOrAboveObserved(
			surface.SelectedThinking,
			expectation.Minimum,
			surface.ThinkingOptions,
		)
	}
	return true
}

func thinkingMenuReady(surface selectionSurface) bool {
	return surface.ThinkingMenuOpen &&
		(len(surface.ThinkingOptions) > 0 || surface.ThinkingSliderReady)
}

func thinkingExpectationForPolicy(
	policy SelectionPolicy,
	surface selectionSurface,
) (thinkingSelectionExpectation, error) {
	expectation := thinkingSelectionExpectation{
		Label:   surface.SelectedThinking,
		Minimum: policy.MinimumThinking,
	}
	if surface.ThinkingSliderReady {
		expectation.SliderMin = surface.ThinkingSliderMin
		expectation.SliderMax = surface.ThinkingSliderMax
		if policy.MinimumThinking != "" {
			minimum, ok := thinkingSliderMinimumValue(
				policy.MinimumThinking,
				surface.ThinkingSliderMin,
				surface.ThinkingSliderMax,
			)
			if !ok {
				return expectation, fmt.Errorf(
					"ChatGPT thinking slider range cannot prove minimum %q",
					policy.MinimumThinking,
				)
			}
			expectation.MinimumSliderTarget = minimum
			expectation.HasMinimumSliderTarget = true
		}
		switch policy.Thinking {
		case ThinkingCurrent:
			// Preserve the observed value only when a floor requires a
			// numeric proof. Current by itself is intentionally observation-only.
		case ThinkingHighest:
			expectation.SliderTarget = surface.ThinkingSliderMax
			expectation.HasSliderTarget = true
		case ThinkingMiddle:
			target, ok := thinkingSliderMiddleValue(
				surface.ThinkingSliderMin,
				surface.ThinkingSliderMax,
			)
			if !ok {
				return expectation, fmt.Errorf(
					"ChatGPT thinking slider midpoint is unavailable",
				)
			}
			expectation.SliderTarget = target
			expectation.HasSliderTarget = true
		default:
			target, ok := thinkingSliderTargetValue(
				policy.Thinking,
				surface.ThinkingSliderMin,
				surface.ThinkingSliderMax,
			)
			if !ok {
				return expectation, fmt.Errorf(
					"ChatGPT thinking slider cannot map requested level %q",
					policy.Thinking,
				)
			}
			expectation.SliderTarget = target
			expectation.HasSliderTarget = true
		}
		// A numeric proof is authoritative even when the product relabels
		// the selected stop. Keep the label only for human-facing output.
		if expectation.HasSliderTarget || expectation.HasMinimumSliderTarget {
			expectation.Label = ""
		}
		return expectation, nil
	}

	switch policy.Thinking {
	case ThinkingCurrent:
		// The initial visible label is the current selection proof.
	case ThinkingHighest:
		option, ok := highestReadyThinkingOption(surface.ThinkingOptions)
		if !ok {
			return expectation, fmt.Errorf(
				"highest ChatGPT thinking option is unavailable",
			)
		}
		expectation.Label = option.Label
	case ThinkingMiddle:
		logical := logicalThinkingOptions(surface.ThinkingOptions)
		if len(logical) == 0 {
			return expectation, fmt.Errorf(
				"ChatGPT thinking midpoint is unavailable",
			)
		}
		expectation.Label = logical[(len(logical)-1)/2].Label
	default:
		expectation.Label = policy.Thinking
	}
	return expectation, nil
}

func selectedThinkingStable(
	surface selectionSurface,
	expectation thinkingSelectionExpectation,
) bool {
	if surface.PickerCount != 1 || strings.TrimSpace(surface.SelectedThinking) == "" {
		return false
	}
	if expectation.Label == "" {
		return true
	}
	return strings.EqualFold(
		surface.SelectedThinking,
		expectation.Label,
	)
}

func verifyThinkingExpectation(
	surface selectionSurface,
	expectation thinkingSelectionExpectation,
	menuRequired bool,
) error {
	if !expectation.matches(surface, menuRequired) {
		if expectation.sliderProofRequired() {
			return fmt.Errorf(
				"ChatGPT thinking slider proof changed before Send",
			)
		}
		return fmt.Errorf(
			"ChatGPT selected thinking %q does not satisfy the requested proof",
			expectation.Label,
		)
	}
	return nil
}

func selectionSurfaceReady(
	current selectionSurface,
	continuation bool,
) bool {
	productsReady := current.ChatCount == 0 &&
		current.WorkCount == 0 &&
		continuation
	if current.ChatCount == 1 && current.WorkCount == 1 {
		productsReady = current.Chat.Ready
	}
	return current.PickerCount == 1 &&
		current.Picker.Ready &&
		strings.TrimSpace(current.SelectedThinking) != "" &&
		productsReady
}

func selectChatGPT(
	ctx context.Context,
	session *cdp.PageSession,
	policy SelectionPolicy,
	continuation bool,
	timeout time.Duration,
	poll time.Duration,
) (selectionObservation, error) {
	observation := selectionObservation{
		ProductMode:        "Chat",
		ProductAction:      "already_selected",
		IntelligenceAction: "already_selected",
		ModelAction:        "not_requested",
	}
	deadline := time.Now().Add(timeout)
	var surface selectionSurface
	if err := pollSelectionSurface(
		ctx,
		session,
		deadline,
		poll,
		&surface,
		func(current selectionSurface) bool {
			return selectionSurfaceReady(current, continuation)
		},
	); err != nil {
		return observation, fmt.Errorf("ChatGPT selection controls not ready: %w", err)
	}
	if surface.SpecializedCount != 0 {
		return observation, fmt.Errorf("specialized ChatGPT conversation surface is active")
	}
	if surface.ChatCount == 1 && !surface.ChatSelected {
		if err := activateSelectionControl(
			ctx,
			session,
			"product",
			"Chat",
		); err != nil {
			return observation, err
		}
		observation.ProductAction = "selected"
		if err := pollSelectionSurface(
			ctx,
			session,
			deadline,
			poll,
			&surface,
			func(current selectionSurface) bool {
				return current.ChatCount == 1 &&
					current.WorkCount == 1 &&
					current.ChatSelected
			},
		); err != nil {
			return observation, fmt.Errorf("Chat product selection was not proven: %w", err)
		}
	}

	needsMenu := policy.Thinking != ThinkingCurrent ||
		policy.MinimumThinking != "" ||
		policy.Model != ModelCurrent
	if needsMenu {
		if err := openThinkingMenu(
			ctx,
			session,
			deadline,
			poll,
			&surface,
		); err != nil {
			return observation, err
		}
		observation.IntelligenceOptions = optionLabels(
			logicalThinkingOptions(surface.ThinkingOptions),
			false,
		)
	}
	expectation, err := thinkingExpectationForPolicy(policy, surface)
	if err != nil {
		return observation, err
	}
	observation.expectation = expectation
	if expectation.HasSliderTarget {
		if surface.ThinkingSliderValue != expectation.SliderTarget {
			if err := activateThinkingSlider(
				ctx,
				session,
				expectation.SliderMin,
				expectation.SliderMax,
				expectation.SliderTarget,
			); err != nil {
				return observation, err
			}
			observation.IntelligenceAction = "selected"
			if err := pollSelectionSurface(
				ctx,
				session,
				deadline,
				poll,
				&surface,
				func(current selectionSurface) bool {
					return current.PickerCount == 1 &&
						expectation.sliderRangeMatches(current) &&
						current.ThinkingSliderValue == expectation.SliderTarget
				},
			); err != nil {
				return observation, fmt.Errorf(
					"ChatGPT thinking slider target %d was not proven: %w",
					expectation.SliderTarget,
					err,
				)
			}
		}
	} else if expectation.Label != "" && !strings.EqualFold(
		surface.SelectedThinking,
		expectation.Label,
	) {
		option, ok := exactOption(surface.ThinkingOptions, expectation.Label)
		if !ok || !option.Ready {
			return observation, fmt.Errorf(
				"ChatGPT thinking option %q is unavailable",
				expectation.Label,
			)
		}
		if err := activateSelectionControl(
			ctx,
			session,
			"option",
			option.Label,
		); err != nil {
			return observation, err
		}
		observation.IntelligenceAction = "selected"
		if err := pollSelectionSurface(
			ctx,
			session,
			deadline,
			poll,
			&surface,
			func(current selectionSurface) bool {
				return current.PickerCount == 1 &&
					expectation.matches(current, false)
			},
		); err != nil {
			return observation, fmt.Errorf(
				"ChatGPT thinking selection %q was not proven: %w",
				expectation.Label,
				err,
			)
		}
	}
	if expectation.requiresThinkingMenu() {
		if !thinkingMenuReady(surface) {
			if err := openThinkingMenu(
				ctx,
				session,
				deadline,
				poll,
				&surface,
			); err != nil {
				return observation, err
			}
		}
		if err := verifyThinkingExpectation(surface, expectation, true); err != nil {
			return observation, fmt.Errorf(
				"ChatGPT thinking minimum or selected value was not proven: %w",
				err,
			)
		}
	}
	observation.Intelligence = surface.SelectedThinking
	observation.ThinkingSelectionMode = policy.Thinking
	if surface.ThinkingSliderReady {
		observation.ThinkingSliderReady = true
		observation.ThinkingSliderMin = surface.ThinkingSliderMin
		observation.ThinkingSliderMax = surface.ThinkingSliderMax
		observation.ThinkingSliderValue = surface.ThinkingSliderValue
	}

	if policy.Model != ModelCurrent {
		if err := openThinkingMenu(
			ctx,
			session,
			deadline,
			poll,
			&surface,
		); err != nil {
			return observation, err
		}
		if err := openModelMenu(
			ctx,
			session,
			deadline,
			poll,
			&surface,
		); err != nil {
			return observation, fmt.Errorf("ChatGPT model options were not observed: %w", err)
		}
		observation.ModelOptions = optionLabels(surface.ModelOptions, true)
		requestedModel := policy.Model
		if policy.Model == ModelHighest {
			option, ok := highestReadyModelOption(surface.ModelOptions)
			if !ok {
				return observation, fmt.Errorf(
					"highest ChatGPT model option is unavailable",
				)
			}
			requestedModel = option.Label
		}
		modelOption, ok := exactOption(surface.ModelOptions, requestedModel)
		if !ok || !modelOption.Ready {
			return observation, fmt.Errorf(
				"ChatGPT model option %q is unavailable",
				requestedModel,
			)
		}
		if modelOption.Checked {
			observation.ModelAction = "already_selected"
			observation.Model = modelOption.Label
		} else {
			if err := activateSelectionControl(
				ctx,
				session,
				"option",
				modelOption.Label,
			); err != nil {
				return observation, err
			}
			observation.ModelAction = "selected"
			if err := verifySelectedModel(
				ctx,
				session,
				deadline,
				poll,
				requestedModel,
				&surface,
			); err != nil {
				return observation, err
			}
			observation.Model = surface.SelectedModel
		}
	}

	if err := closeSelectionMenus(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return observation, err
	}
	if err := observeSelectionSurface(ctx, session, &surface); err != nil {
		return observation, err
	}
	if !selectedThinkingStable(surface, expectation) {
		return observation, fmt.Errorf("ChatGPT thinking changed after selection")
	}
	observation.OK = true
	return observation, nil
}

func closeSelectionMenus(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
	surface *selectionSurface,
) error {
	if err := observeSelectionSurface(ctx, session, surface); err != nil {
		return err
	}
	if !surface.ThinkingMenuOpen && !surface.ModelMenuOpen {
		return nil
	}
	if surface.PickerCount != 1 || !surface.Picker.Ready {
		return fmt.Errorf(
			"exact ChatGPT thinking picker is unavailable while selection menus are open",
		)
	}
	if err := activateSelectionControl(
		ctx,
		session,
		"picker",
		surface.SelectedThinking,
	); err != nil {
		return fmt.Errorf(
			"close ChatGPT selection menus through the exact picker: %w",
			err,
		)
	}
	if err := pollSelectionSurface(
		ctx,
		session,
		deadline,
		poll,
		surface,
		func(current selectionSurface) bool {
			return !current.ThinkingMenuOpen &&
				!current.ModelMenuOpen
		},
	); err != nil {
		return fmt.Errorf(
			"ChatGPT selection menus did not close through the exact picker: %w",
			err,
		)
	}
	return nil
}

func verifySelectionAtSend(
	ctx context.Context,
	session *cdp.PageSession,
	expectedThinking string,
	expectedModel string,
	timeout time.Duration,
	poll time.Duration,
) error {
	return verifySelectionAtSendWithExpectation(
		ctx,
		session,
		thinkingSelectionExpectation{Label: expectedThinking},
		expectedModel,
		timeout,
		poll,
	)
}

func verifySelectionAtSendWithExpectation(
	ctx context.Context,
	session *cdp.PageSession,
	expectation thinkingSelectionExpectation,
	expectedModel string,
	timeout time.Duration,
	poll time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var surface selectionSurface
	// The current ChatGPT composer exposes the default/current effort as a
	// slider inside the picker menu, not as the older menuitemradio list. The
	// current-thinking/current-model path does not need to open that menu: the
	// visible picker label is the authoritative selection proof, and opening
	// it would make a valid fresh composer fail while looking for obsolete
	// option nodes.
	if strings.TrimSpace(expectedModel) == "" && !expectation.requiresThinkingMenu() {
		if err := observeSelectionSurface(ctx, session, &surface); err != nil {
			return err
		}
		if !selectedThinkingStable(surface, expectation) {
			return fmt.Errorf(
				"selected ChatGPT thinking changed from %q to %q",
				expectation.Label,
				surface.SelectedThinking,
			)
		}
		return nil
	}
	if err := openThinkingMenu(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return err
	}
	if err := verifyThinkingExpectation(surface, expectation, true); err != nil {
		return err
	}
	if strings.TrimSpace(expectedModel) != "" {
		if err := openModelMenu(
			ctx,
			session,
			deadline,
			poll,
			&surface,
		); err != nil {
			return fmt.Errorf(
				"ChatGPT model options were not observable before Send: %w",
				err,
			)
		}
		model, ok := exactOption(surface.ModelOptions, expectedModel)
		if !ok || !model.Checked {
			return fmt.Errorf(
				"selected ChatGPT model changed before Send; expected %q",
				expectedModel,
			)
		}
	}
	if err := closeSelectionMenus(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return fmt.Errorf(
			"ChatGPT selection menus did not close before Send: %w",
			err,
		)
	}
	return nil
}

func verifySelectedModel(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
	expected string,
	surface *selectionSurface,
) error {
	if err := openThinkingMenu(
		ctx,
		session,
		deadline,
		poll,
		surface,
	); err != nil {
		return err
	}
	if err := openModelMenu(
		ctx,
		session,
		deadline,
		poll,
		surface,
	); err != nil {
		return fmt.Errorf(
			"ChatGPT model selection %q was not proven: %w",
			expected,
			err,
		)
	}
	if !strings.EqualFold(surface.SelectedModel, expected) {
		return fmt.Errorf(
			"ChatGPT model selection %q was not proven",
			expected,
		)
	}
	return nil
}

// prepareSelectionGuardAtSend performs the final reversible selection proof.
// When an exact model is requested it intentionally leaves the thinking menu
// open: the selected model trigger remains rendered, so the action-pending
// dispatcher can re-observe both thinking and model without another click.
func prepareSelectionGuardAtSend(
	ctx context.Context,
	session *cdp.PageSession,
	intelligence string,
	model string,
	timeout time.Duration,
	poll time.Duration,
) error {
	return prepareSelectionGuardAtSendWithExpectation(
		ctx,
		session,
		thinkingSelectionExpectation{Label: intelligence},
		model,
		timeout,
		poll,
	)
}

func prepareSelectionGuardAtSendWithExpectation(
	ctx context.Context,
	session *cdp.PageSession,
	expectation thinkingSelectionExpectation,
	model string,
	timeout time.Duration,
	poll time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var surface selectionSurface
	if err := pollSelectionSurface(
		ctx,
		session,
		deadline,
		poll,
		&surface,
		func(current selectionSurface) bool {
			return selectedThinkingStable(current, expectation)
		},
	); err != nil {
		return fmt.Errorf(
			"ChatGPT final thinking guard was not observed: %w",
			err,
		)
	}
	if strings.TrimSpace(model) == "" && !expectation.requiresThinkingMenu() {
		return nil
	}
	if err := openThinkingMenu(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return fmt.Errorf(
			"ChatGPT final model guard could not open the thinking menu: %w",
			err,
		)
	}
	if err := verifyThinkingExpectation(surface, expectation, true); err != nil {
		return fmt.Errorf("ChatGPT final thinking guard was not observed: %w", err)
	}
	if strings.TrimSpace(model) == "" {
		// Leave the thinking menu open when the pending action needs a
		// numeric/floor proof. The dispatcher can observe it without another
		// reversible click immediately before Send.
		return nil
	}
	if surface.ModelMenuOpen ||
		surface.ModelTriggerCount != 1 ||
		!strings.EqualFold(surface.ModelTriggerLabel, model) {
		return fmt.Errorf(
			"ChatGPT final model guard %q was not observed",
			model,
		)
	}
	return nil
}

// observeSelectionGuardAtSend is observation-only. Once action_pending is
// durable it never opens, closes, reloads, or reselects a provider control.
func observeSelectionGuardAtSend(
	ctx context.Context,
	session *cdp.PageSession,
	intelligence string,
	model string,
) error {
	return observeSelectionGuardAtSendWithExpectation(
		ctx,
		session,
		thinkingSelectionExpectation{Label: intelligence},
		model,
	)
}

func observeSelectionGuardAtSendWithExpectation(
	ctx context.Context,
	session *cdp.PageSession,
	expectation thinkingSelectionExpectation,
	model string,
) error {
	var surface selectionSurface
	if err := observeSelectionSurface(ctx, session, &surface); err != nil {
		return err
	}
	if expectation.requiresThinkingMenu() {
		if err := verifyThinkingExpectation(surface, expectation, true); err != nil {
			return fmt.Errorf(
				"ChatGPT final thinking guard changed before Send: %w",
				err,
			)
		}
	} else if !selectedThinkingStable(surface, expectation) {
		return fmt.Errorf("ChatGPT final thinking guard changed before Send")
	}
	if strings.TrimSpace(model) == "" {
		return nil
	}
	if !surface.ThinkingMenuOpen ||
		surface.ModelMenuOpen ||
		surface.ModelTriggerCount != 1 ||
		!strings.EqualFold(surface.ModelTriggerLabel, model) {
		return fmt.Errorf(
			"ChatGPT final model guard %q changed before Send",
			model,
		)
	}
	return nil
}

func inspectChatGPTSelectionOptions(
	ctx context.Context,
	session *cdp.PageSession,
	timeout time.Duration,
	poll time.Duration,
) (selectionSurface, error) {
	deadline := time.Now().Add(timeout)
	var surface selectionSurface
	if err := pollSelectionSurface(
		ctx,
		session,
		deadline,
		poll,
		&surface,
		func(current selectionSurface) bool {
			return current.Editor.Ready &&
				current.PickerCount == 1 &&
				current.Picker.Ready &&
				strings.TrimSpace(current.SelectedThinking) != ""
		},
	); err != nil {
		return surface, err
	}
	if err := openThinkingMenu(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return surface, err
	}
	if surface.ModelTriggerCount == 1 && surface.ModelTrigger.Ready {
		if err := openModelMenu(
			ctx,
			session,
			deadline,
			poll,
			&surface,
		); err != nil {
			return surface, err
		}
	}
	observed := surface
	if err := closeSelectionMenus(
		ctx,
		session,
		deadline,
		poll,
		&surface,
	); err != nil {
		return observed, err
	}
	return observed, nil
}

func openThinkingMenu(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
	surface *selectionSurface,
) error {
	var lastErr error
	for time.Now().Before(deadline) {
		if err := observeSelectionSurface(ctx, session, surface); err != nil {
			lastErr = err
		} else if thinkingMenuReady(*surface) {
			return nil
		} else if !surface.ThinkingMenuOpen {
			if surface.PickerCount != 1 || !surface.Picker.Ready {
				lastErr = fmt.Errorf("ChatGPT thinking picker is unavailable")
			} else if err := activateSelectionControl(
				ctx,
				session,
				"picker",
				surface.SelectedThinking,
			); err != nil {
				lastErr = err
			}
		}
		attemptDeadline := selectionAttemptDeadline(deadline, poll)
		if err := pollSelectionSurface(
			ctx,
			session,
			attemptDeadline,
			poll,
			surface,
			func(current selectionSurface) bool {
				return thinkingMenuReady(current)
			},
		); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return fmt.Errorf(
		"ChatGPT thinking options were not observed: %w",
		lastErr,
	)
}

func openModelMenu(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
	surface *selectionSurface,
) error {
	var lastErr error
	for time.Now().Before(deadline) {
		if err := observeSelectionSurface(ctx, session, surface); err != nil {
			lastErr = err
		} else if surface.ModelMenuOpen && len(surface.ModelOptions) > 0 {
			return nil
		} else {
			if !surface.ThinkingMenuOpen {
				if err := openThinkingMenu(
					ctx,
					session,
					selectionAttemptDeadline(deadline, poll),
					poll,
					surface,
				); err != nil {
					lastErr = err
					continue
				}
			}
			if surface.ModelTriggerCount != 1 ||
				!surface.ModelTrigger.Ready {
				lastErr = fmt.Errorf("ChatGPT model menu is unavailable")
			} else if err := activateSelectionControl(
				ctx,
				session,
				"model-trigger",
				surface.ModelTriggerLabel,
			); err != nil {
				lastErr = err
			}
		}
		attemptDeadline := selectionAttemptDeadline(deadline, poll)
		if err := pollSelectionSurface(
			ctx,
			session,
			attemptDeadline,
			poll,
			surface,
			func(current selectionSurface) bool {
				return current.ModelMenuOpen &&
					len(current.ModelOptions) > 0
			},
		); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return fmt.Errorf(
		"ChatGPT model options were not observed: %w",
		lastErr,
	)
}

func selectionAttemptDeadline(
	overall time.Time,
	poll time.Duration,
) time.Time {
	window := poll * 4
	if window < time.Second {
		window = time.Second
	}
	if window > 3*time.Second {
		window = 3 * time.Second
	}
	attempt := time.Now().Add(window)
	if overall.Before(attempt) {
		return overall
	}
	return attempt
}

func activateThinkingSlider(
	ctx context.Context,
	session *cdp.PageSession,
	expectedMin int,
	expectedMax int,
	target int,
) error {
	minJSON, err := json.Marshal(expectedMin)
	if err != nil {
		return fmt.Errorf("encode ChatGPT thinking slider minimum")
	}
	maxJSON, err := json.Marshal(expectedMax)
	if err != nil {
		return fmt.Errorf("encode ChatGPT thinking slider maximum")
	}
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("encode ChatGPT thinking slider target")
	}
	var activated struct {
		OK        bool `json:"ok"`
		Count     int  `json:"count"`
		Activated bool `json:"activated"`
	}
	expression := fmt.Sprintf(`(async () => {
  const expectedMin = %s;
  const expectedMax = %s;
  const target = %s;
  const visible = element => {
    if (!(element instanceof HTMLElement)) return false;
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
  };
  const numberAttribute = (element, name) => {
    const value = Number(element.getAttribute(name));
    return Number.isInteger(value) ? value : null;
  };
  const accessibleName = element => String(
    element && (
      element.getAttribute('aria-label') ||
      element.getAttribute('aria-valuetext') ||
      element.textContent || ''
    ) || ''
  ).replace(/\s+/g, ' ').trim();
  const menus = Array.from(document.querySelectorAll('[role="menu"]'))
    .filter(visible);
  const semantic = menus.flatMap(menu => Array.from(
    menu.querySelectorAll('[role="slider"]')
  )).filter(slider => visible(slider) &&
    numberAttribute(slider, 'aria-valuemin') !== null &&
    numberAttribute(slider, 'aria-valuemax') !== null &&
    numberAttribute(slider, 'aria-valuenow') !== null);
  const named = semantic.filter(slider =>
    /reason|thinking|effort/i.test(accessibleName(slider)) ||
    Boolean(slider.closest('[data-model-reasoning-effort-slider]'))
  );
  const candidates = named.length > 0 ? named : semantic;
  if (candidates.length !== 1 || target < expectedMin || target > expectedMax) {
    return {ok: false, count: candidates.length, activated: false};
  }
  const initial = candidates[0];
  if (numberAttribute(initial, 'aria-valuemin') !== expectedMin ||
      numberAttribute(initial, 'aria-valuemax') !== expectedMax) {
    return {ok: false, count: 1, activated: false};
  }
  const findSlider = () => {
    const liveMenus = Array.from(document.querySelectorAll('[role="menu"]'))
      .filter(visible);
    const live = liveMenus.flatMap(menu => Array.from(
      menu.querySelectorAll('[role="slider"]')
    )).filter(slider => visible(slider) &&
      numberAttribute(slider, 'aria-valuemin') !== null &&
      numberAttribute(slider, 'aria-valuemax') !== null &&
      numberAttribute(slider, 'aria-valuenow') !== null);
    const liveNamed = live.filter(slider =>
      /reason|thinking|effort/i.test(accessibleName(slider)) ||
      Boolean(slider.closest('[data-model-reasoning-effort-slider]'))
    );
    const current = liveNamed.length > 0 ? liveNamed : live;
    return current.length === 1 ? current[0] : null;
  };
  let current = numberAttribute(initial, 'aria-valuenow');
  if (current === null || current < expectedMin || current > expectedMax) {
    return {ok: false, count: 1, activated: false};
  }
  for (let step = 0; step <= expectedMax - expectedMin && current !== target; step++) {
    const live = findSlider();
    if (!live) break;
    live.focus();
    const key = target > current ? 'ArrowRight' : 'ArrowLeft';
    live.dispatchEvent(new KeyboardEvent('keydown', {
      key,
      code: key,
      bubbles: true,
      cancelable: true
    }));
    // The product replaces the range node after a key press. Re-observe the
    // semantic range after one render turn rather than retaining stale DOM.
    await new Promise(resolve => setTimeout(resolve, 50));
    const refreshed = findSlider();
    current = refreshed ? numberAttribute(refreshed, 'aria-valuenow') : null;
    if (current === null) break;
  }
  return {
    ok: current === target,
    count: 1,
    activated: current === target,
    slider: true,
    min: expectedMin,
    max: expectedMax,
    value: current
  };
})()`, minJSON, maxJSON, targetJSON)
	if err := evaluateInto(ctx, session, expression, &activated); err != nil {
		return fmt.Errorf("activate ChatGPT thinking slider: %w", err)
	}
	if !activated.OK || !activated.Activated {
		return fmt.Errorf(
			"ChatGPT thinking slider target %d was not activated (count %d)",
			target,
			activated.Count,
		)
	}
	return nil
}

func pollSelectionSurface(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
	surface *selectionSurface,
	ready func(selectionSurface) bool,
) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.DeadlineExceeded
	}
	_, err := pollUntil(
		ctx,
		remaining,
		poll,
		func() (bool, error) {
			if err := observeSelectionSurface(
				ctx,
				session,
				surface,
			); err != nil {
				return false, err
			}
			return ready(*surface), nil
		},
	)
	return err
}

func activateSelectionControl(
	ctx context.Context,
	session *cdp.PageSession,
	kind string,
	expectedLabel string,
) error {
	kindJSON, err := json.Marshal(kind)
	if err != nil {
		return fmt.Errorf("encode ChatGPT selection control kind")
	}
	labelJSON, err := json.Marshal(expectedLabel)
	if err != nil {
		return fmt.Errorf("encode ChatGPT selection control label")
	}
	var activated struct {
		OK        bool `json:"ok"`
		Count     int  `json:"count"`
		Activated bool `json:"activated"`
	}
	expression := fmt.Sprintf(`(async () => {
	  const kind = %s;
	  const expected = String(%s || '').trim().toLowerCase();
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 &&
	      rect.width > 0 && rect.height > 0;
	  };
	  const label = element => String(
	    element && (
	      element.innerText ||
	      element.textContent ||
	      element.getAttribute('aria-label') ||
	      ''
	    ) || ''
	  ).replace(/\s+/g, ' ').trim();
	  const enabled = element =>
	    visible(element) && !element.disabled &&
	    element.getAttribute('aria-disabled') !== 'true';
	  let candidates = [];
	  switch (kind) {
	  case 'editor': {
	    const editors = Array.from(document.querySelectorAll(
	      '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	    )).filter((element, index, values) =>
	      values.indexOf(element) === index &&
	      element.isContentEditable &&
	      enabled(element)
	    );
	    candidates = editors;
	    break;
	  }
	  case 'editor-tool': {
	    const editors = Array.from(document.querySelectorAll(
	      '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	    )).filter((element, index, values) =>
	      values.indexOf(element) === index &&
	      element.isContentEditable && enabled(element) &&
	      Array.from(element.querySelectorAll(
	        '[data-inline-selection-pill][data-keyword]'
	      )).some(pill => label(pill).toLowerCase() === expected)
	    );
	    candidates = editors;
	    break;
	  }
	  case 'tool-menu':
	    candidates = Array.from(document.querySelectorAll(
	      'button#composer-plus-btn'
	    )).filter(element => enabled(element));
	    break;
	  case 'tool':
	    candidates = Array.from(document.querySelectorAll(
	      'div[tabindex="0"][data-fill]'
	    )).filter(element => {
	      const popover = element.closest('div[class*="popover"]');
	      const first = element.querySelector('span.max-w-full,span');
	      return enabled(element) && Boolean(popover) && visible(popover) &&
	        label(first).toLowerCase() === expected;
	    });
	    break;
	  case 'product':
	    candidates = Array.from(document.querySelectorAll(
	      'button[role="radio"]'
	    )).filter(element =>
	      enabled(element) && label(element).toLowerCase() === expected
	    );
	    break;
	  case 'picker':
	    candidates = Array.from(document.querySelectorAll(
	      'button[aria-haspopup="menu"]'
	    )).filter(element =>
	      enabled(element) &&
	      label(element).toLowerCase() === expected &&
	      (/reason|thinking|effort/i.test(
	        String(element.getAttribute('aria-label') || '')
	      ) || ['Instant', 'Instant 5.5', 'Medium', 'High', 'Extra High', 'Pro']
	        .some(value => value.toLowerCase() === label(element).toLowerCase()))
	    );
	    break;
	  case 'model-trigger':
	    candidates = Array.from(document.querySelectorAll(
	      '[role="menu"] [role="menuitem"]'
	    )).filter(element =>
	      enabled(element) &&
	      element.closest('[role="menu"]') === element.parentElement.closest('[role="menu"]') &&
	      label(element).toLowerCase() === expected
	    );
	    break;
	  case 'option':
	    candidates = Array.from(document.querySelectorAll(
	      '[role="menu"] [role="menuitemradio"]'
	    )).filter(element =>
	      enabled(element) &&
	      label(element).toLowerCase() === expected
	    );
	    if (candidates.length === 0) {
	      const numberAttribute = (element, name) => {
	        const value = Number(element.getAttribute(name));
	        return Number.isInteger(value) ? value : null;
	      };
	      const accessibleName = element => String(
	        element && (
	          element.getAttribute('aria-label') ||
	          element.getAttribute('aria-valuetext') ||
	          element.textContent || ''
	        ) || ''
	      ).replace(/\s+/g, ' ').trim();
	      const findSlider = () => {
	        const semanticSliders = Array.from(document.querySelectorAll(
	          '[role="menu"] [role="slider"]'
	        )).filter(element => {
	          const menu = element.closest('[role="menu"]');
	          return Boolean(menu) && visible(menu) && visible(element) &&
	            numberAttribute(element, 'aria-valuemin') !== null &&
	            numberAttribute(element, 'aria-valuemax') !== null &&
	            numberAttribute(element, 'aria-valuenow') !== null;
	        });
	        const markedSliders = semanticSliders.filter(element =>
	          Boolean(element.closest('[data-model-reasoning-effort-slider]')) ||
	          /reason|thinking|effort/i.test(accessibleName(element)) ||
	          /reason|thinking|effort/i.test(
	            accessibleName(element.closest('[role="menu"]'))
	          )
	        );
	        const sliders = markedSliders.length > 0 ? markedSliders : semanticSliders;
	        return sliders.length === 1 ? sliders[0] : null;
      };
      const slider = findSlider();
      if (slider) {
	        const canonical = [
	          'Instant', 'Instant 5.5', 'Medium', 'High', 'Extra High', 'Pro'
	        ];
	        const minimum = numberAttribute(slider, 'aria-valuemin');
	        const maximum = numberAttribute(slider, 'aria-valuemax');
	        if (minimum === null || maximum === null) break;
	        const values = maximum - minimum + 1 === canonical.length - 1 ?
	          canonical.slice(1) : canonical.slice(0, maximum + 1);
	        const target = values.findIndex(value =>
	          value.toLowerCase() === expected
	        );
	        const targetValue = target + minimum;
	        let current = numberAttribute(slider, 'aria-valuenow');
	        if (target >= 0 && Number.isInteger(current) &&
	            targetValue <= maximum && current >= minimum && current <= maximum) {
	          for (let step = 0; step < values.length && current !== targetValue; step++) {
	            const live = findSlider();
	            if (!live) break;
	            live.focus();
	            const key = targetValue > current ? 'ArrowRight' : 'ArrowLeft';
	            live.dispatchEvent(new KeyboardEvent('keydown', {
	              key,
	              code: key,
	              bubbles: true,
	              cancelable: true
	            }));
	            // ChatGPT replaces the slider node after each key press. Re-query
	            // only after the replacement has had a render turn to commit.
	            await new Promise(resolve => setTimeout(resolve, 50));
	            const refreshed = findSlider();
	            current = refreshed ? numberAttribute(
	              refreshed,
	              'aria-valuenow'
	            ) : null;
	          }
	          return {
	            ok: current === targetValue,
	            count: 1,
	            activated: current === targetValue,
	            slider: true
	          };
	        }
	      }
	    }
	    break;
	  }
	  if (candidates.length !== 1) {
	    return {ok: false, count: candidates.length, activated: false};
	  }
	  const control = candidates[0];
	  const rect = control.getBoundingClientRect();
	  const left = Math.max(0, rect.left);
	  const right = Math.min(window.innerWidth, rect.right);
	  const topEdge = Math.max(0, rect.top);
	  const bottom = Math.min(window.innerHeight, rect.bottom);
	  if (right <= left || bottom <= topEdge) {
	    return {ok: false, count: 1, activated: false};
	  }
	  const fractions = [0.5, 0.75, 0.25, 0.9, 0.1];
	  for (const yFraction of fractions) {
	    for (const xFraction of fractions) {
	      const x = left + (right - left) * xFraction;
	      const y = topEdge + (bottom - topEdge) * yFraction;
	      const hit = document.elementFromPoint(x, y);
	      const editorForm = kind === 'editor' ?
	        control.closest('form') : null;
	      const sameEditorFormProxy = kind === 'editor' &&
	        editorForm &&
	        hit instanceof HTMLElement &&
	        hit.contains(control) &&
	        hit.closest('form') === editorForm;
	      if (hit && (
	        hit === control ||
	        control.contains(hit) ||
	        sameEditorFormProxy
	      )) {
	        const down = {
	          bubbles: true,
	          cancelable: true,
	          composed: true,
	          button: 0,
	          buttons: 1,
	          clientX: x,
	          clientY: y,
	          pointerId: 1,
	          pointerType: 'mouse',
	          isPrimary: true
	        };
	        const up = {...down, buttons: 0};
	        control.focus();
	        control.dispatchEvent(new PointerEvent('pointerdown', down));
	        control.dispatchEvent(new MouseEvent('mousedown', down));
	        control.dispatchEvent(new PointerEvent('pointerup', up));
	        control.dispatchEvent(new MouseEvent('mouseup', up));
	        control.dispatchEvent(new MouseEvent('click', up));
	        return {
	          ok: true,
	          count: 1,
	          activated: true
	        };
	      }
	    }
	  }
	  return {ok: false, count: 1, activated: false};
	})()`, kindJSON, labelJSON)
	if err := evaluateInto(ctx, session, expression, &activated); err != nil {
		return fmt.Errorf("activate exact ChatGPT %s control: %w", kind, err)
	}
	if !activated.OK || !activated.Activated {
		return fmt.Errorf(
			"exact ChatGPT %s control count was %d",
			kind,
			activated.Count,
		)
	}
	return nil
}

func exactOption(
	options []selectableOption,
	label string,
) (selectableOption, bool) {
	var found selectableOption
	count := 0
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Label), label) {
			found = option
			count++
		}
	}
	return found, count == 1
}

func optionLabels(options []selectableOption, reverse bool) []string {
	labels := make([]string, 0, len(options))
	for _, option := range options {
		if value := strings.TrimSpace(option.Label); value != "" &&
			!containsString(labels, value) {
			labels = append(labels, value)
		}
	}
	if reverse {
		for left, right := 0, len(labels)-1; left < right; left, right =
			left+1, right-1 {
			labels[left], labels[right] = labels[right], labels[left]
		}
	}
	return labels
}

func logicalThinkingOptions(options []selectableOption) []selectableOption {
	logical := make([]selectableOption, 0, len(options))
	hasUnknown := false
	for _, option := range options {
		if strings.TrimSpace(option.Label) == "" ||
			containsOption(logical, option.Label) {
			continue
		}
		logical = append(logical, option)
		if thinkingRank(option.Label) < 0 {
			hasUnknown = true
		}
	}
	if hasUnknown {
		switch observedThinkingDirection(logical) {
		case 1:
			return logical
		case -1:
			reverseOptions(logical)
			return logical
		}
	}
	sort.SliceStable(logical, func(left, right int) bool {
		leftRank := thinkingRank(logical[left].Label)
		rightRank := thinkingRank(logical[right].Label)
		switch {
		case leftRank >= 0 && rightRank >= 0:
			return leftRank < rightRank
		case leftRank >= 0:
			return true
		case rightRank >= 0:
			return false
		default:
			return false
		}
	})
	return logical
}

func observedThinkingDirection(options []selectableOption) int {
	previous := -1
	direction := 0
	known := 0
	for _, option := range options {
		rank := thinkingRank(option.Label)
		if rank < 0 {
			continue
		}
		known++
		if previous >= 0 && rank != previous {
			currentDirection := 1
			if rank < previous {
				currentDirection = -1
			}
			if direction != 0 && direction != currentDirection {
				return 0
			}
			direction = currentDirection
		}
		previous = rank
	}
	if known < 2 {
		return 0
	}
	return direction
}

func reverseOptions(options []selectableOption) {
	for left, right := 0, len(options)-1; left < right; left, right =
		left+1, right-1 {
		options[left], options[right] = options[right], options[left]
	}
}

func thinkingAtOrAboveObserved(
	selected string,
	minimum string,
	options []selectableOption,
) bool {
	if thinkingRank(selected) >= 0 {
		return thinkingAtOrAbove(selected, minimum)
	}
	minimumRank := thinkingRank(minimum)
	if minimumRank < 0 || observedThinkingDirection(options) == 0 {
		return false
	}
	logical := logicalThinkingOptions(options)
	selectedIndex := optionIndexFold(logical, selected)
	if selectedIndex < 0 {
		return false
	}
	for index := 0; index <= selectedIndex; index++ {
		if thinkingRank(logical[index].Label) >= minimumRank {
			return true
		}
	}
	return false
}

func highestReadyThinkingOption(
	options []selectableOption,
) (selectableOption, bool) {
	logical := logicalThinkingOptions(options)
	for index := len(logical) - 1; index >= 0; index-- {
		if logical[index].Ready {
			return logical[index], true
		}
	}
	return selectableOption{}, false
}

func highestReadyModelOption(
	providerOrder []selectableOption,
) (selectableOption, bool) {
	for _, option := range providerOrder {
		if option.Ready && strings.TrimSpace(option.Label) != "" {
			return option, true
		}
	}
	return selectableOption{}, false
}

func optionIndexFold(options []selectableOption, candidate string) int {
	for index, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Label), candidate) {
			return index
		}
	}
	return -1
}

func containsOption(options []selectableOption, candidate string) bool {
	return optionIndexFold(options, candidate) >= 0
}

func observeSelectionSurface(
	ctx context.Context,
	session *cdp.PageSession,
	surface *selectionSurface,
) error {
	return evaluateInto(ctx, session, `(() => {
  const visible = element => {
    if (!(element instanceof HTMLElement)) return false;
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
  };
  const label = element => String(
    element && (
      element.innerText ||
      element.textContent ||
      element.getAttribute('aria-label') ||
      ''
    ) || ''
  ).replace(/\s+/g, ' ').trim();
	  const equal = (left, right) =>
	    String(left || '').toLowerCase() === String(right || '').toLowerCase();
	  const accessibleName = element => {
	    if (!element) return '';
	    const labelledBy = String(
	      element.getAttribute('aria-labelledby') || ''
	    ).split(/\s+/).filter(Boolean).map(id =>
	      document.getElementById(id)?.innerText ||
	      document.getElementById(id)?.textContent || ''
	    ).join(' ');
	    return String(
	      element.getAttribute('aria-label') || labelledBy ||
	      element.getAttribute('title') || ''
	    ).replace(/\s+/g, ' ').trim();
	  };
	  const integerAttribute = (element, name) => {
	    const value = Number(element?.getAttribute(name));
	    return Number.isInteger(value) ? value : null;
	  };
	  const directItems = (menu, role) => menu ? Array.from(
	    menu.querySelectorAll('[role="' + role + '"]')
	  ).filter(item =>
	    visible(item) && item.closest('[role="menu"]') === menu
	  ) : [];
	  const semanticSlider = menu => {
	    if (!menu) return null;
	    const sliders = Array.from(menu.querySelectorAll('[role="slider"]'))
	      .filter(slider => visible(slider) &&
        integerAttribute(slider, 'aria-valuemin') !== null &&
        integerAttribute(slider, 'aria-valuemax') !== null &&
        integerAttribute(slider, 'aria-valuenow') !== null);
	    const named = sliders.filter(slider =>
	      Boolean(slider.closest('[data-model-reasoning-effort-slider]')) ||
	      /reason|thinking|effort/i.test(accessibleName(slider)) ||
	      /reason|thinking|effort/i.test(accessibleName(menu))
	    );
	    const candidates = named.length > 0 ? named : sliders;
	    return candidates.length === 1 ? candidates[0] : null;
	  };
  const actionable = element => {
    if (!element || !visible(element) || element.disabled ||
        element.getAttribute('aria-disabled') === 'true') {
      return {ready: false, x: -1, y: -1};
    }
    const rect = element.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    const top = document.elementFromPoint(x, y);
    return {
      ready: Boolean(top && (top === element || element.contains(top))),
      x,
      y
    };
  };
  const productRadios = Array.from(document.querySelectorAll(
    'button[role="radio"]'
  )).filter(visible);
  const chats = productRadios.filter(button => label(button) === 'Chat');
  const works = productRadios.filter(button => label(button) === 'Work');
  const specialized = Array.from(document.querySelectorAll(
    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
    '[data-testid*="deep-research"][aria-pressed="true"],' +
    '[data-testid*="agent"][aria-pressed="true"]'
  ));
  const thinkingKnown = [
    'Instant', 'Instant 5.5', 'Medium', 'High', 'Extra High', 'Pro'
  ];
	  const pickers = () => Array.from(document.querySelectorAll(
	    'button[aria-haspopup="menu"]'
	  )).filter(button =>
	    visible(button) && (
	      /reason|thinking|effort/i.test(accessibleName(button)) ||
	      thinkingKnown.some(item => equal(item, label(button)))
	    )
  );
  const picker = pickers().length === 1 ? pickers()[0] : null;
  const selectedThinking = picker ? label(picker) : '';
  const menus = Array.from(document.querySelectorAll('[role="menu"]')).filter(visible);
	  const thinkingMenus = menus.filter(menu => {
	    const legacyOptions = directItems(menu, 'menuitemradio').some(option =>
	      thinkingKnown.some(item =>
	        item.toLowerCase() === label(option).toLowerCase()
	      )
	    );
	    const slider = semanticSlider(menu);
	    return legacyOptions || Boolean(slider);
  });
  const thinkingMenu = thinkingMenus.length === 1 ? thinkingMenus[0] : null;
  const modelMenus = menus.filter(menu =>
    menu !== thinkingMenu &&
    directItems(menu, 'menuitemradio').length > 0
  );
  const modelMenu = modelMenus.length === 1 ? modelMenus[0] : null;
  const toOption = element => {
    const action = actionable(element);
    return {
      label: label(element),
      checked: element.getAttribute('aria-checked') === 'true',
      ready: action.ready,
      x: action.x,
      y: action.y
    };
  };
  const legacyThinkingOptions = directItems(
    thinkingMenu,
    'menuitemradio'
  ).map(toOption);
	  const thinkingSlider = semanticSlider(thinkingMenu);
	  const thinkingSliderMin = thinkingSlider ? integerAttribute(
	    thinkingSlider, 'aria-valuemin'
	  ) : null;
	  const thinkingSliderMax = thinkingSlider ? integerAttribute(
	    thinkingSlider, 'aria-valuemax'
	  ) : null;
	  const thinkingSliderValue = thinkingSlider ? integerAttribute(
	    thinkingSlider, 'aria-valuenow'
	  ) : null;
  const thinkingSliderStopCount = thinkingSliderMin !== null &&
	    thinkingSliderMax !== null ? thinkingSliderMax - thinkingSliderMin + 1 : 0;
  const thinkingSliderLabels = thinkingSliderStopCount ===
	    thinkingKnown.length - 1 ? thinkingKnown.slice(1) :
	    thinkingSliderStopCount === thinkingKnown.length ? thinkingKnown : [];
  const thinkingSliderReady = Boolean(
    thinkingSlider &&
	    thinkingSliderMin !== null &&
	    thinkingSliderMax !== null &&
	    thinkingSliderMax >= thinkingSliderMin &&
	    thinkingSliderValue !== null &&
	    thinkingSliderValue >= thinkingSliderMin &&
	    thinkingSliderValue <= thinkingSliderMax &&
	    visible(thinkingSlider)
  );
	  const thinkingSliderLabel = thinkingSlider ? String(
	    thinkingSlider.getAttribute('aria-valuetext') ||
	    label(thinkingSlider) || ''
	  ).trim() : '';
  const thinkingOptions = legacyThinkingOptions.length > 0 ?
    legacyThinkingOptions : thinkingSliderReady ?
    thinkingSliderLabels.map((option, index) => ({
      label: option,
	    checked: thinkingSliderMin + index === thinkingSliderValue,
      ready: true,
      x: -1,
      y: -1
    })) : [];
  const modelTriggers = directItems(thinkingMenu, 'menuitem');
  const modelOptions = directItems(modelMenu, 'menuitemradio').map(toOption);
  const selectedModels = modelOptions.filter(option => option.checked);
  const editor = document.querySelector('#prompt-textarea') ||
    document.querySelector('[contenteditable="true"][role="textbox"]');
  return {
    editor: actionable(editor),
    chat_count: chats.length,
    work_count: works.length,
    chat_selected: chats.length === 1 &&
      chats[0].getAttribute('aria-checked') === 'true',
    chat: actionable(chats.length === 1 ? chats[0] : null),
    specialized_count: specialized.length,
    picker_count: pickers().length,
    picker: actionable(picker),
    selected_thinking: selectedThinking,
    thinking_menu_open: Boolean(thinkingMenu),
    thinking_options: thinkingOptions,
	    thinking_slider_ready: thinkingSliderReady,
	    thinking_slider_min: thinkingSliderReady ? thinkingSliderMin : -1,
	    thinking_slider_max: thinkingSliderReady ? thinkingSliderMax : -1,
	    thinking_slider_value: thinkingSliderReady ? thinkingSliderValue : -1,
	    thinking_slider_label: thinkingSliderLabel,
    model_trigger_count: modelTriggers.length,
    model_trigger: actionable(
      modelTriggers.length === 1 ? modelTriggers[0] : null
    ),
    model_trigger_label: modelTriggers.length === 1 ?
      label(modelTriggers[0]) : '',
    model_menu_open: Boolean(modelMenu),
    model_options_provider_order: modelOptions,
    selected_model: selectedModels.length === 1 ?
      selectedModels[0].label : ''
  };
})()`, surface)
}
