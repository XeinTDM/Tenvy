export type UserRole = 'viewer' | 'operator' | 'developer' | 'admin';

export type AuthenticatedUser = {
	id: string;
	role: UserRole;
	passkeyRegistered: boolean;
	voucherId: string;
	voucherActive: boolean;
	voucherExpiresAt: Date | null;
};
