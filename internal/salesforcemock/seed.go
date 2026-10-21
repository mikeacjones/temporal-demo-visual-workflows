package salesforcemock

// seed fills the org with a small book of business. Close dates are relative
// to today, so the same opportunities are always overdue. Acme has two
// overdue deals: updating both at once locks Acme twice, which shows a retry.
func (o *Org) seed() {
	day := func(offset int) string { return o.now().AddDate(0, 0, offset).Format("2006-01-02") }
	for object := range prefixes {
		o.records[object] = map[string]record{}
	}
	account := func(name, domain, industry, city string, revenue float64) string {
		return o.insert("Account", record{
			"Name": name, "Domain__c": domain, "Industry": industry, "BillingCity": city,
			"AnnualRevenue": revenue, "Type": "Customer - Direct",
		})
	}
	acme := account("Acme Corporation", "acme.com", "Manufacturing", "Phoenix", 250_000_000)
	initech := account("Initech", "initech.com", "Technology", "Austin", 48_000_000)
	umbrella := account("Umbrella Health", "umbrella.health", "Healthcare", "Raccoon City", 900_000_000)
	stark := account("Stark Industries", "stark.com", "Energy", "Los Angeles", 1_200_000_000)
	wayne := account("Wayne Enterprises", "wayne.com", "Transportation", "Gotham", 700_000_000)
	hooli := account("Hooli", "hooli.xyz", "Technology", "Palo Alto", 3_000_000_000)

	contact := func(accountID, first, last, email, title string) {
		o.insert("Contact", record{"AccountId": accountID, "FirstName": first, "LastName": last, "Email": email, "Title": title})
	}
	contact(acme, "Wile", "Coyote", "wile@acme.com", "Head of Procurement")
	contact(acme, "Road", "Runner", "road@acme.com", "CFO")
	contact(initech, "Bill", "Lumbergh", "bill@initech.com", "VP Operations")
	contact(umbrella, "Alice", "Abernathy", "alice@umbrella.health", "CIO")
	contact(stark, "Pepper", "Potts", "pepper@stark.com", "CEO")
	contact(wayne, "Lucius", "Fox", "lucius@wayne.com", "CTO")
	contact(hooli, "Gavin", "Belson", "gavin@hooli.xyz", "CEO")

	opportunity := func(accountID, name, stage string, amount float64, closeIn int) {
		o.insert("Opportunity", record{
			"AccountId": accountID, "Name": name, "StageName": stage, "Amount": amount,
			"CloseDate": day(closeIn), "NextStep": nil, "LeadSource": "Web",
		})
	}
	// Open and overdue: what the pipeline sweep finds.
	opportunity(acme, "Acme — Anvils, bulk renewal", "Negotiation/Review", 84_000, -12)
	opportunity(acme, "Acme — Rocket skates pilot", "Proposal/Price Quote", 23_500, -3)
	opportunity(initech, "Initech — TPS reporting suite", "Qualification", 41_000, -30)
	opportunity(umbrella, "Umbrella — Lab automation", "Prospecting", 310_000, -7)
	opportunity(wayne, "Wayne — Fleet telematics", "Needs Analysis", 128_000, -1)
	// Open and on track.
	opportunity(stark, "Stark — Arc reactor monitoring", "Proposal/Price Quote", 560_000, 21)
	opportunity(hooli, "Hooli — Nucleus compression", "Prospecting", 95_000, 45)
	opportunity(umbrella, "Umbrella — Cold storage", "Qualification", 72_000, 14)
	// Closed.
	opportunity(stark, "Stark — Workshop sensors", "Closed Won", 210_000, -40)
	opportunity(initech, "Initech — Printer fleet", "Closed Lost", 18_000, -60)

	kase := func(accountID, subject, priority, status string) {
		o.insert("Case", record{"AccountId": accountID, "Subject": subject, "Priority": priority, "Status": status, "Origin": "Email"})
	}
	kase(acme, "Anvil shipment arrived dented", "High", "Working")
	kase(acme, "Invoice shows the wrong PO number", "Low", "New")
	kase(initech, "Stapler integration not syncing", "Medium", "New")
	kase(stark, "Sensor firmware question", "Low", "Closed")
	kase(hooli, "SSO login loop", "High", "Escalated")

	o.insert("Lead", record{"FirstName": "Dolores", "LastName": "Abernathy", "Company": "Delos", "Email": "dolores@delos.com", "Status": "Open - Not Contacted"})
	o.insert("Lead", record{"FirstName": "Richard", "LastName": "Hendricks", "Company": "Pied Piper", "Email": "richard@piedpiper.com", "Status": "Working - Contacted"})
}
