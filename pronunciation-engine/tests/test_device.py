"""Chọn thiết bị chạy forward pass.

Logic thuần, test được không cần GPU: mọi nhánh quan trọng là quyết định dựa trên
`torch.cuda.is_available()`, và giá trị đó giả lập được.
"""

from __future__ import annotations

import pytest
import torch

from app.config import Settings


def test_cpu_is_explicit(monkeypatch):
    monkeypatch.setattr(torch.cuda, "is_available", lambda: True)
    # Yêu cầu cpu thì dùng cpu, kể cả khi có GPU. Người vận hành có thể muốn vậy để so
    # sánh hoặc để nhường GPU cho tiến trình khác.
    assert Settings(device="cpu").resolved_device() == "cpu"


def test_auto_picks_gpu_when_present(monkeypatch):
    monkeypatch.setattr(torch.cuda, "is_available", lambda: True)
    assert Settings(device="auto").resolved_device() == "cuda"


def test_auto_falls_back_to_cpu(monkeypatch):
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    assert Settings(device="auto").resolved_device() == "cpu"


def test_explicit_cuda_without_gpu_raises(monkeypatch):
    """Đây là test quan trọng nhất trong file.

    Đặt `PE_DEVICE=cuda` mà rơi về CPU trong im lặng nghĩa là trả tiền thuê GPU nhưng chạy
    bằng CPU — và chỉ phát hiện khi nhìn hoá đơn, hoặc khi thắc mắc vì sao vẫn chậm y như
    cũ. Nguyên nhân phổ biến nhất: dựng image bằng `Dockerfile` (PyTorch CPU-only) thay vì
    `Dockerfile.gpu`. Phải hỏng TO TIẾNG ngay lúc khởi động.
    """
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    with pytest.raises(RuntimeError, match="cuda"):
        Settings(device="cuda").resolved_device()


def test_unknown_device_raises():
    # `mps` (Apple GPU) chưa hỗ trợ — nhận nhầm rồi rơi về cpu sẽ che mất ý định.
    with pytest.raises(ValueError, match="không hợp lệ"):
        Settings(device="mps").resolved_device()


def test_threads_only_matter_on_cpu():
    # Trên GPU, `torch_threads` không được dùng để siết luồng (xem loader.load). Test này
    # chỉ khẳng định hàm tính luồng vẫn trả giá trị hợp lệ, không phụ thuộc thiết bị.
    assert Settings(torch_threads=4).resolved_torch_threads() == 4
    assert Settings(torch_threads=0).resolved_torch_threads() >= 1
