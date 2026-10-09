package ownership

// ResolveQueueModeHold is who lifts a merge queue admission held because
// neither the harness's queue nor the forge's can land into the target as its
// protection stands. A repository setting is a person's act, so a hold whose
// remedy is one, recorded and valid, is the operator's; any other hold is the
// development manager's to replan, because what it waits on is work or the
// project's own configuration rather than something only a person can do.
func ResolveQueueModeHold(remedy *PersonOnlyRemedy) Mover {
	if remedy != nil && remedy.Validate() == nil {
		return MoverOperator
	}
	return MoverDevelopmentManager
}
