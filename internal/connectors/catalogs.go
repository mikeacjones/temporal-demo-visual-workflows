package connectors

// The curated catalogs: the fields of common Salesforce objects and
// ServiceNow tables, with their API names, types, picklists, and what a create
// requires. Objects and tables not listed here still work; their fields are
// mapped by name, as custom fields are.

func ro(name, typ, desc string) Field {
	return Field{Name: name, Type: typ, ReadOnly: true, Description: desc}
}

var (
	sfID        = ro("Id", String, "The record's 18-character ID.")
	sfCreated   = ro("CreatedDate", DateTime, "")
	sfModified  = ro("LastModifiedDate", DateTime, "")
	sfOwner     = Field{Name: "OwnerId", Type: String, Description: "The owning user's ID."}
	sfLeadSrc   = Field{Name: "LeadSource", Type: String, Enum: []string{"Web", "Phone Inquiry", "Partner Referral", "Purchased List", "Other"}}
	sfIndustry  = Field{Name: "Industry", Type: String, Enum: []string{"Agriculture", "Banking", "Chemicals", "Communications", "Construction", "Education", "Energy", "Healthcare", "Manufacturing", "Retail", "Technology", "Transportation", "Other"}}
	sfRating    = Field{Name: "Rating", Type: String, Enum: []string{"Hot", "Warm", "Cold"}}
	sfDesc      = Field{Name: "Description", Type: String, Multiline: true}
	sfAccountID = Field{Name: "AccountId", Type: String, Description: "The Account's ID."}
)

var salesforceCatalog = map[string][]Field{
	"Account": {
		sfID,
		{Name: "Name", Type: String, Required: true},
		{Name: "Domain__c", Type: String, Description: "Custom field: the company's web domain, an external ID."},
		{Name: "Type", Type: String, Enum: []string{"Prospect", "Customer - Direct", "Customer - Channel", "Partner", "Other"}},
		sfIndustry,
		{Name: "AnnualRevenue", Type: Number},
		{Name: "NumberOfEmployees", Type: Integer},
		sfRating,
		{Name: "Phone", Type: String},
		{Name: "Website", Type: String},
		{Name: "AccountNumber", Type: String},
		{Name: "BillingStreet", Type: String},
		{Name: "BillingCity", Type: String},
		{Name: "BillingState", Type: String},
		{Name: "BillingPostalCode", Type: String},
		{Name: "BillingCountry", Type: String},
		sfDesc, sfOwner, sfCreated, sfModified,
	},
	"Contact": {
		sfID,
		{Name: "FirstName", Type: String},
		{Name: "LastName", Type: String, Required: true},
		{Name: "Email", Type: String},
		sfAccountID,
		{Name: "Title", Type: String},
		{Name: "Department", Type: String},
		{Name: "Phone", Type: String},
		{Name: "MobilePhone", Type: String},
		sfLeadSrc,
		{Name: "MailingCity", Type: String},
		{Name: "MailingCountry", Type: String},
		sfDesc, sfOwner, sfCreated, sfModified,
	},
	"Lead": {
		sfID,
		{Name: "FirstName", Type: String},
		{Name: "LastName", Type: String, Required: true},
		{Name: "Company", Type: String, Required: true},
		{Name: "Email", Type: String},
		{Name: "Title", Type: String},
		{Name: "Phone", Type: String},
		{Name: "Status", Type: String, Enum: []string{"Open - Not Contacted", "Working - Contacted", "Closed - Converted", "Closed - Not Converted"}},
		sfLeadSrc, sfIndustry, sfRating,
		{Name: "AnnualRevenue", Type: Number},
		{Name: "NumberOfEmployees", Type: Integer},
		{Name: "Website", Type: String},
		sfDesc, sfOwner, sfCreated, sfModified,
	},
	"Opportunity": {
		sfID,
		{Name: "Name", Type: String, Required: true},
		sfAccountID,
		{Name: "StageName", Type: String, Required: true, Enum: []string{"Prospecting", "Qualification", "Needs Analysis", "Value Proposition", "Id. Decision Makers", "Perception Analysis", "Proposal/Price Quote", "Negotiation/Review", "Closed Won", "Closed Lost"}},
		{Name: "CloseDate", Type: Date, Required: true},
		{Name: "Amount", Type: Number},
		{Name: "Probability", Type: Number, Description: "Percent, 0 to 100."},
		{Name: "Type", Type: String, Enum: []string{"New Customer", "Existing Customer - Upgrade", "Existing Customer - Replacement", "Existing Customer - Downgrade"}},
		sfLeadSrc,
		{Name: "NextStep", Type: String},
		sfDesc, sfOwner,
		ro("IsClosed", Boolean, ""), ro("IsWon", Boolean, ""), sfCreated, sfModified,
	},
	"Case": {
		sfID,
		{Name: "Subject", Type: String},
		{Name: "Status", Type: String, Enum: []string{"New", "Working", "Escalated", "Closed"}},
		{Name: "Priority", Type: String, Enum: []string{"High", "Medium", "Low"}},
		{Name: "Origin", Type: String, Enum: []string{"Phone", "Email", "Web"}},
		{Name: "Type", Type: String, Enum: []string{"Question", "Problem", "Feature Request"}},
		{Name: "Reason", Type: String},
		sfAccountID,
		{Name: "ContactId", Type: String},
		sfDesc, sfOwner,
		ro("IsClosed", Boolean, ""), sfCreated, sfModified,
	},
	"Task": {
		sfID,
		{Name: "Subject", Type: String},
		{Name: "Status", Type: String, Enum: []string{"Not Started", "In Progress", "Completed", "Waiting on someone else", "Deferred"}},
		{Name: "Priority", Type: String, Enum: []string{"High", "Normal", "Low"}},
		{Name: "ActivityDate", Type: Date, Description: "When it's due."},
		{Name: "WhatId", Type: String, Description: "The record it's about: an Account, Opportunity, Case, …"},
		{Name: "WhoId", Type: String, Description: "The Contact or Lead it involves."},
		{Name: "Type", Type: String, Enum: []string{"Call", "Email", "Meeting", "Other"}},
		sfDesc, sfOwner,
		ro("IsClosed", Boolean, ""), sfCreated, sfModified,
	},
}

func choice(name string, values, labels []string, desc string) Field {
	return Field{Name: name, Type: String, Enum: values, EnumLabels: labels, Description: desc}
}

var (
	snSysIDField = ro("sys_id", String, "The record's unique ID.")
	snNumber     = ro("number", String, "")
	snCreatedOn  = ro("sys_created_on", DateTime, "")
	snUpdatedOn  = ro("sys_updated_on", DateTime, "")
	snThree      = []string{"1", "2", "3"}
	snThreeNames = []string{"1 - High", "2 - Medium", "3 - Low"}
	snShortDesc  = Field{Name: "short_description", Type: String, Required: true}
	snDesc       = Field{Name: "description", Type: String, Multiline: true}
	snCI         = Field{Name: "cmdb_ci", Type: String, Description: "The configuration item: the service or system affected."}
	snGroup      = Field{Name: "assignment_group", Type: String}
	snAssigned   = Field{Name: "assigned_to", Type: String}
	snPriority   = Field{Name: "priority", Type: String, ReadOnly: true, Description: "Worked out from urgency and impact.", Enum: []string{"1", "2", "3", "4", "5"}, EnumLabels: []string{"1 - Critical", "2 - High", "3 - Moderate", "4 - Low", "5 - Planning"}}
)

var serviceNowCatalog = map[string][]Field{
	"incident": {
		snSysIDField, snNumber, snShortDesc, snDesc,
		choice("urgency", snThree, snThreeNames, ""),
		choice("impact", snThree, snThreeNames, ""),
		snPriority,
		choice("state", []string{"1", "2", "3", "6", "7", "8"}, []string{"New", "In Progress", "On Hold", "Resolved", "Closed", "Canceled"}, "Resolved and Closed need a resolution code and notes."),
		choice("category", []string{"inquiry", "software", "hardware", "network", "database"}, nil, ""),
		{Name: "subcategory", Type: String},
		snCI,
		{Name: "caller_id", Type: String},
		snGroup, snAssigned,
		choice("close_code", []string{"Solved (Work Around)", "Solved (Permanently)", "Solved Remotely (Work Around)", "Solved Remotely (Permanently)", "Not Solved (Not Reproducible)", "Not Solved (Too Costly)", "Closed/Resolved by Caller"}, nil, "The resolution code."),
		{Name: "close_notes", Type: String, Multiline: true, Description: "The resolution notes."},
		{Name: "work_notes", Type: String, Multiline: true},
		{Name: "comments", Type: String, Multiline: true, Description: "Additional comments, visible to the caller."},
		ro("resolved_at", DateTime, ""), ro("closed_at", DateTime, ""), ro("active", String, "true while open."),
		snCreatedOn, snUpdatedOn,
	},
	"change_request": {
		snSysIDField, snNumber, snShortDesc, snDesc,
		choice("type", []string{"standard", "normal", "emergency"}, nil, ""),
		choice("state", []string{"-5", "-4", "-3", "-2", "-1", "0", "3", "4"}, []string{"New", "Assess", "Authorize", "Scheduled", "Implement", "Review", "Closed", "Canceled"}, ""),
		choice("risk", []string{"1", "2", "3", "4"}, []string{"Very High", "High", "Moderate", "Low"}, ""),
		choice("impact", snThree, snThreeNames, ""),
		snPriority, snCI,
		{Name: "start_date", Type: DateTime},
		{Name: "end_date", Type: DateTime},
		snGroup, snAssigned,
		{Name: "justification", Type: String, Multiline: true},
		{Name: "implementation_plan", Type: String, Multiline: true},
		{Name: "backout_plan", Type: String, Multiline: true},
		snCreatedOn, snUpdatedOn,
	},
	"problem": {
		snSysIDField, snNumber, snShortDesc, snDesc,
		choice("urgency", snThree, snThreeNames, ""),
		choice("impact", snThree, snThreeNames, ""),
		snPriority, snCI,
		{Name: "cause_notes", Type: String, Multiline: true},
		{Name: "fix_notes", Type: String, Multiline: true},
		{Name: "known_error", Type: Boolean},
		snGroup, snAssigned, snCreatedOn, snUpdatedOn,
	},
	"sys_user": {
		snSysIDField,
		{Name: "user_name", Type: String, Required: true},
		{Name: "first_name", Type: String},
		{Name: "last_name", Type: String},
		{Name: "name", Type: String},
		{Name: "email", Type: String},
		{Name: "title", Type: String},
		{Name: "department", Type: String},
		{Name: "phone", Type: String},
		snCreatedOn, snUpdatedOn,
	},
}
