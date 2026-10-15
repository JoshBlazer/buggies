export function applyDiscount(user, price) {
  if (user.tier === 'gold') return price * 0.8;
  return price;
}
