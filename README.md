# go-kvm

A minimal KVM hypervisor written in Go.
Runs x86 guest code in real mode using the Linux KVM API directly — no QEMU, no libraries.

Go로 작성한 최소한의 KVM 하이퍼바이저.
Linux KVM API를 직접 사용하여 x86 게스트 코드를 리얼모드로 실행합니다. QEMU나 외부 라이브러리 없이 동작합니다.

## What it does / 동작

- Creates a VM and vCPU via `/dev/kvm`
- Allocates 1MB guest memory and writes x86 machine code directly
- Guest outputs "Hello, KVM!" via port I/O (0x3F8)
- Host catches VM-Exit and prints each character

## Requirements / 요구사항

- Linux with KVM support (`/dev/kvm`)
- Go 1.21+
- `sudo` (for `/dev/kvm` access)

## Run / 실행

```bash
go build -o kvm-hello .
sudo ./kvm-hello
```

## References / 참고

- [KVM API](https://docs.kernel.org/virt/kvm/api.html)
- [dpw/kvm-hello-world](https://github.com/dpw/kvm-hello-world) (C reference)
