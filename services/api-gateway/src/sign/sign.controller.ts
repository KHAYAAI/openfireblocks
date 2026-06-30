import { Body, Controller, HttpCode, HttpStatus, Post } from '@nestjs/common';
import { SignService } from './sign.service';
import { SignRequestDto } from './dto/sign-request.dto';

// POST /sign: sign (and optionally broadcast) an Ethereum transaction.
@Controller('sign')
export class SignController {
  constructor(private readonly signService: SignService) {}

  @Post()
  @HttpCode(HttpStatus.OK)
  async sign(@Body() req: SignRequestDto) {
    return this.signService.sign(req);
  }
}
