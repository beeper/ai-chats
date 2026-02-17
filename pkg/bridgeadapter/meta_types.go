package bridgeadapter

import "maunium.net/go/mautrix/bridgev2/database"

// MetaTypes builds a database.MetaTypes registration map from constructor functions.
func MetaTypes(
	portalFn func() any,
	messageFn func() any,
	userLoginFn func() any,
	ghostFn func() any,
) database.MetaTypes {
	return database.MetaTypes{
		Portal:    portalFn,
		Message:   messageFn,
		UserLogin: userLoginFn,
		Ghost:     ghostFn,
	}
}
