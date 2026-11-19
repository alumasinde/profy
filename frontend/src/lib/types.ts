export interface Challenge {
	challenge_id: string;
	channel: string;
	destination_masked: string;
	expires_in: number;
	resend_after: number;
}
