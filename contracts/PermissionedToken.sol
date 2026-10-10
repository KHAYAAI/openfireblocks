// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.24;

/// A permissioned (security) token: an ERC-20 whose transfers only work between holders the
/// issuer has admitted, with the controls a regulated issuer needs and a plain ERC-20 lacks.
///
/// The owner is meant to be an organisation's threshold-signed key, so every administrative
/// act below is a signed transaction that went through that organisation's approval quorum.
/// The contract itself does not know that; it only knows who the owner is.
///
/// What it enforces on every transfer: the contract is not paused, and neither party is
/// frozen, and both parties are admitted holders. What the owner can do beyond that:
/// admit and remove holders, freeze, pause, mint up to a fixed cap, burn (redemption), and
/// force a transfer between holders (recovery of lost keys, court orders). Those last powers
/// are the point of a regulated token and also the reason the owner must be a governed key.
contract PermissionedToken {
    string public name;
    string public symbol;
    uint8 public immutable decimals;
    uint256 public immutable supplyCap;
    uint256 public totalSupply;

    address public owner;
    address public pendingOwner;
    bool public paused;

    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    mapping(address => bool) public isHolder;
    mapping(address => bool) public isFrozen;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);
    event HolderAdded(address indexed holder);
    event HolderRemoved(address indexed holder);
    event Frozen(address indexed holder);
    event Unfrozen(address indexed holder);
    event Paused();
    event Unpaused();
    event ForcedTransfer(address indexed from, address indexed to, uint256 value);
    event OwnershipProposed(address indexed proposed);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    modifier onlyOwner() {
        require(msg.sender == owner, "not the owner");
        _;
    }

    constructor(string memory name_, string memory symbol_, uint8 decimals_, uint256 supplyCap_, address owner_) {
        require(owner_ != address(0), "owner is the zero address");
        require(supplyCap_ > 0, "supply cap must be positive");
        name = name_;
        symbol = symbol_;
        decimals = decimals_;
        supplyCap = supplyCap_;
        owner = owner_;
        emit OwnershipTransferred(address(0), owner_);
    }

    // ---- ownership: two steps, so a typo cannot hand the token to nobody ----

    function proposeOwner(address next) external onlyOwner {
        require(next != address(0), "owner is the zero address");
        pendingOwner = next;
        emit OwnershipProposed(next);
    }

    function acceptOwnership() external {
        require(msg.sender == pendingOwner, "not the proposed owner");
        emit OwnershipTransferred(owner, msg.sender);
        owner = msg.sender;
        pendingOwner = address(0);
    }

    // ---- who may hold it ----

    function addHolder(address holder) external onlyOwner {
        require(holder != address(0), "zero address");
        require(!isHolder[holder], "already a holder");
        isHolder[holder] = true;
        emit HolderAdded(holder);
    }

    /// A holder with a balance cannot be removed: move or burn the balance first, so tokens
    /// are never stranded at an address that is no longer allowed to move them.
    function removeHolder(address holder) external onlyOwner {
        require(isHolder[holder], "not a holder");
        require(balanceOf[holder] == 0, "holder still has a balance");
        isHolder[holder] = false;
        emit HolderRemoved(holder);
    }

    function freeze(address holder) external onlyOwner {
        require(!isFrozen[holder], "already frozen");
        isFrozen[holder] = true;
        emit Frozen(holder);
    }

    function unfreeze(address holder) external onlyOwner {
        require(isFrozen[holder], "not frozen");
        isFrozen[holder] = false;
        emit Unfrozen(holder);
    }

    function pause() external onlyOwner {
        require(!paused, "already paused");
        paused = true;
        emit Paused();
    }

    function unpause() external onlyOwner {
        require(paused, "not paused");
        paused = false;
        emit Unpaused();
    }

    // ---- supply ----

    function mint(address to, uint256 amount) external onlyOwner {
        require(isHolder[to], "recipient is not an admitted holder");
        require(totalSupply + amount <= supplyCap, "exceeds the supply cap");
        totalSupply += amount;
        balanceOf[to] += amount;
        emit Transfer(address(0), to, amount);
    }

    /// Redemption: the issuer removes tokens from a holder's balance.
    function burn(address from, uint256 amount) external onlyOwner {
        require(balanceOf[from] >= amount, "balance too low");
        balanceOf[from] -= amount;
        totalSupply -= amount;
        emit Transfer(from, address(0), amount);
    }

    /// Recovery and legal orders. Works while paused and on frozen accounts, because those are
    /// exactly the situations it exists for; the recipient must still be an admitted holder.
    function forceTransfer(address from, address to, uint256 amount) external onlyOwner {
        require(isHolder[to], "recipient is not an admitted holder");
        require(balanceOf[from] >= amount, "balance too low");
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
        emit ForcedTransfer(from, to, amount);
    }

    // ---- ordinary transfers, with the rules ----

    /// Whether a transfer would be allowed, and if not, why. For wallets and for the platform's
    /// own pre-checks; the rules themselves are enforced in _move.
    function canTransfer(address from, address to, uint256 amount) public view returns (bool ok, string memory reason) {
        if (paused) return (false, "the token is paused");
        if (!isHolder[from]) return (false, "sender is not an admitted holder");
        if (!isHolder[to]) return (false, "recipient is not an admitted holder");
        if (isFrozen[from]) return (false, "sender is frozen");
        if (isFrozen[to]) return (false, "recipient is frozen");
        if (balanceOf[from] < amount) return (false, "balance too low");
        return (true, "");
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        _move(msg.sender, to, amount);
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        uint256 allowed = allowance[from][msg.sender];
        require(allowed >= amount, "allowance too low");
        if (allowed != type(uint256).max) {
            allowance[from][msg.sender] = allowed - amount;
        }
        _move(from, to, amount);
        return true;
    }

    function _move(address from, address to, uint256 amount) internal {
        (bool ok, string memory reason) = canTransfer(from, to, amount);
        require(ok, reason);
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
    }
}
