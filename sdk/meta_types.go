package sdk

import "maunium.net/go/mautrix/bridgev2/database"

func BuildMetaTypes(portal, message, userLogin, ghost func() any) database.MetaTypes {
	return database.MetaTypes{
		Portal:    portal,
		Message:   message,
		UserLogin: userLogin,
		Ghost:     ghost,
	}
}
